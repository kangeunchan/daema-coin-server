package server

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const fcmMessagingScope = "https://www.googleapis.com/auth/firebase.messaging"

type fcmClient struct {
	httpClient     *http.Client
	projectID      string
	serviceAccount fcmServiceAccount

	mu          sync.Mutex
	accessToken string
	expiresAt   time.Time
}

type fcmServiceAccount struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	ProjectID   string `json:"project_id"`
	TokenURI    string `json:"token_uri"`
}

type fcmNotification struct {
	Body  string `json:"body,omitempty"`
	Title string `json:"title,omitempty"`
}

type fcmWebpushOptions struct {
	Link string `json:"link,omitempty"`
}

type fcmWebpush struct {
	FCMOptions fcmWebpushOptions `json:"fcm_options,omitempty"`
}

type fcmMessage struct {
	Data         map[string]string `json:"data,omitempty"`
	Notification fcmNotification   `json:"notification,omitempty"`
	Token        string            `json:"token,omitempty"`
	Webpush      *fcmWebpush       `json:"webpush,omitempty"`
}

type fcmSendResponse struct {
	Name string `json:"name"`
}

type fcmSendError struct {
	Body       string
	StatusCode int
}

func (e fcmSendError) Error() string {
	return fmt.Sprintf("fcm send failed with status %d: %s", e.StatusCode, truncateLogString(e.Body))
}

func newFCMClientFromEnv() *fcmClient {
	if !envBool("FCM_ENABLED", true) {
		return nil
	}

	account, ok := fcmServiceAccountFromEnv()
	if !ok {
		return nil
	}
	if account.TokenURI == "" {
		account.TokenURI = "https://oauth2.googleapis.com/token"
	}
	projectID := firstNonEmpty(env("FCM_PROJECT_ID", ""), account.ProjectID)
	if projectID == "" || account.ClientEmail == "" || account.PrivateKey == "" {
		slog.Warn("fcm disabled because service account is incomplete")
		return nil
	}

	return &fcmClient{
		httpClient: &http.Client{
			Timeout: envDuration("FCM_HTTP_TIMEOUT", 5*time.Second),
		},
		projectID:      projectID,
		serviceAccount: account,
	}
}

func fcmServiceAccountFromEnv() (fcmServiceAccount, bool) {
	raw := strings.TrimSpace(os.Getenv("FCM_SERVICE_ACCOUNT_JSON"))
	if raw == "" {
		if encoded := strings.TrimSpace(os.Getenv("FCM_SERVICE_ACCOUNT_JSON_BASE64")); encoded != "" {
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				slog.Warn("decode FCM_SERVICE_ACCOUNT_JSON_BASE64", "error", err)
				return fcmServiceAccount{}, false
			}
			raw = string(decoded)
		}
	}
	if raw == "" {
		filePath := firstNonEmpty(env("FCM_SERVICE_ACCOUNT_FILE", ""), env("GOOGLE_APPLICATION_CREDENTIALS", ""))
		if filePath != "" {
			body, err := os.ReadFile(filePath)
			if err != nil {
				slog.Warn("read fcm service account file", "path", filePath, "error", err)
				return fcmServiceAccount{}, false
			}
			raw = string(body)
		}
	}
	if raw == "" {
		return fcmServiceAccount{}, false
	}

	var account fcmServiceAccount
	if err := json.Unmarshal([]byte(raw), &account); err != nil {
		slog.Warn("parse fcm service account json", "error", err)
		return fcmServiceAccount{}, false
	}
	return account, true
}

func (c *fcmClient) Send(ctx context.Context, target string, message fcmMessage) (string, error) {
	if c == nil {
		return "", errors.New("fcm is not configured")
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return "", errors.New("fcm target is required")
	}
	message.Token = target

	accessToken, err := c.accessTokenForRequest(ctx)
	if err != nil {
		return "", err
	}

	body, err := json.Marshal(map[string]any{"message": message})
	if err != nil {
		return "", err
	}
	endpoint := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", url.PathEscape(c.projectID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
	if readErr != nil {
		return "", readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fcmSendError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}
	var out fcmSendResponse
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &out); err != nil {
			return "", err
		}
	}
	return out.Name, nil
}

func (c *fcmClient) accessTokenForRequest(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.accessToken != "" && time.Now().Before(c.expiresAt.Add(-2*time.Minute)) {
		token := c.accessToken
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	token, expiresAt, err := c.mintAccessToken(ctx)
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	c.accessToken = token
	c.expiresAt = expiresAt
	c.mu.Unlock()
	return token, nil
}

func (c *fcmClient) mintAccessToken(ctx context.Context) (string, time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(time.Hour)
	claims := map[string]any{
		"iss":   c.serviceAccount.ClientEmail,
		"scope": fcmMessagingScope,
		"aud":   c.serviceAccount.TokenURI,
		"iat":   now.Unix(),
		"exp":   expiresAt.Unix(),
	}
	assertion, err := signedServiceAccountJWT(claims, c.serviceAccount.PrivateKey)
	if err != nil {
		return "", time.Time{}, err
	}

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", assertion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.serviceAccount.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
	if readErr != nil {
		return "", time.Time{}, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", time.Time{}, fmt.Errorf("mint fcm access token failed with status %d: %s", resp.StatusCode, truncateLogString(string(body)))
	}

	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", time.Time{}, err
	}
	if payload.AccessToken == "" {
		return "", time.Time{}, errors.New("fcm access token response did not include access_token")
	}
	if payload.ExpiresIn > 0 {
		expiresAt = now.Add(time.Duration(payload.ExpiresIn) * time.Second)
	}
	return payload.AccessToken, expiresAt, nil
}

func signedServiceAccountJWT(claims map[string]any, privateKeyPEM string) (string, error) {
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	encodedHeader, err := encodeJWTPart(header)
	if err != nil {
		return "", err
	}
	encodedClaims, err := encodeJWTPart(claims)
	if err != nil {
		return "", err
	}
	unsigned := encodedHeader + "." + encodedClaims

	privateKey, err := parseRSAPrivateKey(privateKeyPEM)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func encodeJWTPart(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func parseRSAPrivateKey(privateKeyPEM string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return nil, errors.New("service account private key is not PEM encoded")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
		return nil, errors.New("service account private key is not RSA")
	}
	rsaKey, pkcs1Err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if pkcs1Err == nil {
		return rsaKey, nil
	}
	return nil, err
}
