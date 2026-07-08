package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type oauthState struct {
	Value         string
	Role          string
	RedirectAfter string
	ExpiresAt     time.Time
}

const (
	roleCustomer = "customer"
	roleAdmin    = "admin"
	roleBooth    = "booth"
	roleTeacher  = "teacher"
)

type authSession struct {
	Token             string    `json:"token"`
	GitHubAccessToken string    `json:"-"`
	User              authUser  `json:"user"`
	Role              string    `json:"role"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

type authUser struct {
	ID        string   `json:"id"`
	GitHubID  int64    `json:"githubId"`
	Login     string   `json:"login"`
	Name      string   `json:"name,omitempty"`
	Email     string   `json:"email,omitempty"`
	AvatarURL string   `json:"avatarUrl,omitempty"`
	HTMLURL   string   `json:"htmlUrl,omitempty"`
	AccountID string   `json:"accountId,omitempty"`
	BoothID   string   `json:"boothId,omitempty"`
	Provider  string   `json:"provider,omitempty"`
	Roles     []string `json:"roles"`
}

type internalAccount struct {
	ID                  string `json:"id"`
	LoginID             string `json:"loginId"`
	PasswordHash        string `json:"passwordHash"`
	Role                string `json:"role"`
	Status              string `json:"status"`
	DisplayName         string `json:"displayName,omitempty"`
	BoothID             string `json:"boothId,omitempty"`
	ForcePasswordChange bool   `json:"forcePasswordChange"`
	CreatedBy           string `json:"createdBy,omitempty"`
	CreatedAt           string `json:"createdAt,omitempty"`
	UpdatedAt           string `json:"updatedAt,omitempty"`
	LastLoginAt         string `json:"lastLoginAt,omitempty"`
}

type internalAccountInput struct {
	LoginID             string
	Password            string
	Role                string
	DisplayName         string
	BoothID             string
	ForcePasswordChange bool
	CreatedBy           string
}

func (s *server) handleGitHubLogin(w http.ResponseWriter, r *http.Request) {
	if !s.githubAuth.Configured() {
		s.fail(w, r, http.StatusServiceUnavailable, "GITHUB_OAUTH_NOT_CONFIGURED", "GitHub OAuth 환경변수가 설정되지 않았습니다.", map[string]any{"required": []string{"GITHUB_OAUTH_CLIENT_ID", "GITHUB_OAUTH_CLIENT_SECRET", "GITHUB_OAUTH_REDIRECT_URI"}})
		return
	}

	state := randomToken()
	role := roleCustomer
	redirectAfter := safeRedirectURL(r.URL.Query().Get("redirectAfter"))

	expiresAt := time.Now().Add(10 * time.Minute)
	if err := s.store.saveOAuthState(r.Context(), oauthState{Value: state, Role: role, RedirectAfter: redirectAfter, ExpiresAt: expiresAt}); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "OAuth state를 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}

	authorizeURL := s.githubAuth.AuthorizeURL(state)
	if r.URL.Query().Get("format") == "json" {
		s.ok(w, r, map[string]any{"provider": "github", "authorizeUrl": authorizeURL, "state": state, "role": role, "expiresAt": expiresAt.Format(time.RFC3339)})
		return
	}
	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

func (s *server) handleGitHubCallback(w http.ResponseWriter, r *http.Request) {
	if errMessage := r.URL.Query().Get("error"); errMessage != "" {
		s.fail(w, r, http.StatusBadRequest, "GITHUB_OAUTH_ERROR", errMessage, map[string]any{"description": r.URL.Query().Get("error_description")})
		return
	}

	session, redirectAfter, err := s.completeGitHubOAuth(r.Context(), r.URL.Query().Get("code"), r.URL.Query().Get("state"), "")
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "GITHUB_OAUTH_FAILED", err.Error(), nil)
		return
	}

	setSessionCookie(w, session)
	redirectURL := appendQuery(redirectAfter, map[string]string{"login": "success", "role": session.Role})
	if installURL, shouldInstall := s.githubAppInstallRedirectURL(r.Context(), session, redirectURL); shouldInstall {
		http.Redirect(w, r, installURL, http.StatusFound)
		return
	}
	// #nosec G710 -- redirectURL is constrained by safeRedirectURL before it is stored in OAuth state.
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (s *server) handleGitHubExchange(w http.ResponseWriter, r *http.Request) {
	req := struct {
		Code  string `json:"code"`
		State string `json:"state"`
		Role  string `json:"role"`
	}{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.fail(w, r, http.StatusBadRequest, "INVALID_REQUEST", "요청 본문을 읽을 수 없습니다.", nil)
		return
	}

	session, _, err := s.completeGitHubOAuth(r.Context(), req.Code, req.State, req.Role)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "GITHUB_OAUTH_FAILED", err.Error(), nil)
		return
	}

	setSessionCookie(w, session)
	s.created(w, r, map[string]any{"accessToken": session.Token, "tokenType": "Bearer", "expiresAt": session.ExpiresAt.Format(time.RFC3339), "user": session.User, "role": session.Role})
}

func (s *server) handleGitHubSession(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "GitHub 로그인이 필요합니다.", nil)
		return
	}
	if sessionHasRole(session, roleTeacher) {
		if err := s.ensureTeacherCustomerProfile(r.Context(), session.User); err != nil {
			s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "교사용 테스트 고객 프로필을 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
			return
		}
		s.ok(w, r, map[string]any{"status": "authenticated"})
		return
	}
	if found, err := s.store.customerProfileExists(r.Context(), session.User.ID); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "학생 프로필을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	} else if !found {
		s.ok(w, r, map[string]any{"status": "profile_required"})
		return
	}
	if err := s.grantSignupBonus(r.Context(), session.User); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "가입 보상을 지급하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	s.ok(w, r, map[string]any{"status": "authenticated"})
}

func (s *server) handleStudentProfile(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "GitHub 로그인이 필요합니다.", nil)
		return
	}
	profile, ok := s.requestPayload(w, r)
	if !ok {
		return
	}
	profile["userId"] = session.User.ID
	profile["githubLogin"] = session.User.Login
	profile["githubId"] = session.User.GitHubID
	item, err := s.store.saveCustomerProfile(r.Context(), session.User, profile)
	if err != nil {
		if errors.Is(err, errStudentNoRequired) || errors.Is(err, errInvalidStudentNo) {
			s.fail(w, r, http.StatusBadRequest, "INVALID_STUDENT_NUMBER", "학번은 숫자 4~12자리로 입력해 주세요.", nil)
			return
		}
		if errors.Is(err, errDuplicateStudentNo) {
			s.fail(w, r, http.StatusConflict, "STUDENT_NUMBER_ALREADY_REGISTERED", "이미 등록된 학번입니다.", nil)
			return
		}
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "학생 프로필을 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	if err := s.grantSignupBonus(r.Context(), session.User); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "가입 보상을 지급하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	s.ok(w, r, item)
}

func (s *server) completeGitHubOAuth(ctx context.Context, code, state, roleOverride string) (authSession, string, error) {
	if !s.githubAuth.Configured() {
		return authSession{}, "", errors.New("GitHub OAuth 환경변수가 설정되지 않았습니다")
	}
	if strings.TrimSpace(code) == "" {
		return authSession{}, "", errors.New("code가 비어 있습니다")
	}

	stateItem, hasState, err := s.store.oauthState(ctx, state)
	if err != nil {
		return authSession{}, "", err
	}
	if hasState {
		_ = s.store.deleteOAuthState(ctx, state)
	}
	if !hasState {
		return authSession{}, "", errors.New("state가 없거나 이미 사용되었습니다")
	}
	if time.Now().After(stateItem.ExpiresAt) {
		return authSession{}, "", errors.New("state가 만료되었습니다")
	}

	githubToken, err := s.githubAuth.Exchange(ctx, code)
	if err != nil {
		return authSession{}, "", err
	}
	githubUser, err := s.githubAuth.User(ctx, githubToken.AccessToken)
	if err != nil {
		return authSession{}, "", err
	}
	email, err := s.githubAuth.PrimaryEmail(ctx, githubToken.AccessToken)
	if err == nil && email != "" {
		githubUser.Email = email
	}

	role := roleCustomer
	_ = roleOverride
	user := authUser{
		ID:        "github-" + strconv.FormatInt(githubUser.ID, 10),
		GitHubID:  githubUser.ID,
		Login:     githubUser.Login,
		Name:      githubUser.Name,
		Email:     githubUser.Email,
		AvatarURL: githubUser.AvatarURL,
		HTMLURL:   githubUser.HTMLURL,
		Provider:  "github",
		Roles:     rolesForGitHubUser(role, githubUser.Email, githubUser.Login),
	}
	if !containsString(user.Roles, role) {
		return authSession{}, "", fmt.Errorf("%s 권한으로 로그인할 수 없는 GitHub 계정입니다", role)
	}

	session := authSession{Token: randomToken(), GitHubAccessToken: githubToken.AccessToken, User: user, Role: role, ExpiresAt: time.Now().Add(24 * time.Hour)}
	if err := s.store.saveSession(ctx, session); err != nil {
		return authSession{}, "", err
	}

	return session, stateItem.RedirectAfter, nil
}

func (s *server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "로그인이 필요합니다.", nil)
		return
	}
	s.ok(w, r, map[string]any{"user": session.User, "role": session.Role, "expiresAt": session.ExpiresAt.Format(time.RFC3339)})
}

func (s *server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if token := bearerToken(r); token != "" {
		_ = s.store.deleteSession(r.Context(), token)
	}
	if cookie, err := r.Cookie("daema_session"); err == nil {
		_ = s.store.deleteSession(r.Context(), cookie.Value)
	}
	http.SetCookie(w, sessionCookie("", time.Time{}, -1))
	s.ok(w, r, map[string]any{"loggedOut": true})
}

func (s *server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	s.handleInternalLogin(w, r, roleAdmin)
}

func (s *server) handleTeacherLogin(w http.ResponseWriter, r *http.Request) {
	s.handleInternalLogin(w, r, roleTeacher)
}

func (s *server) handleInternalLogin(w http.ResponseWriter, r *http.Request, requiredRole string) {
	body, ok := s.requestPayload(w, r)
	if !ok {
		return
	}
	loginID := firstNonEmpty(stringValue(body["loginId"]), stringValue(body["username"]), stringValue(body["login"]))
	password := stringValue(body["password"])
	if loginID == "" || password == "" {
		s.fail(w, r, http.StatusBadRequest, "INVALID_CREDENTIALS", "계정 ID와 비밀번호가 필요합니다.", nil)
		return
	}

	account, found, err := s.store.internalAccountByLogin(r.Context(), loginID)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "계정을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	if !found || !checkPassword(account.PasswordHash, password) {
		s.fail(w, r, http.StatusUnauthorized, "INVALID_CREDENTIALS", "계정 ID 또는 비밀번호가 올바르지 않습니다.", nil)
		return
	}
	if account.Status != "active" {
		s.fail(w, r, http.StatusForbidden, "ACCOUNT_DISABLED", "비활성화된 계정입니다.", nil)
		return
	}
	if normalizeInternalRole(account.Role) != requiredRole {
		s.fail(w, r, http.StatusForbidden, "ROLE_NOT_ALLOWED", "해당 로그인 화면에서 사용할 수 없는 계정입니다.", nil)
		return
	}
	if requiredRole == roleTeacher && strings.EqualFold(strings.TrimSpace(account.CreatedBy), "bootstrap") {
		s.fail(w, r, http.StatusForbidden, "ROLE_NOT_ALLOWED", "티처 계정은 관리자 콘솔에서 발급해야 합니다.", nil)
		return
	}

	account.LastLoginAt = time.Now().UTC().Format(time.RFC3339)
	if _, err := s.store.saveInternalAccount(r.Context(), account); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "로그인 상태를 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}

	if normalizeInternalRole(account.Role) == roleTeacher {
		if err := s.ensureTeacherCustomerProfile(r.Context(), authUser{
			ID:       account.ID,
			Login:    account.LoginID,
			Name:     envDefault(account.DisplayName, account.LoginID),
			Provider: "internal",
			Roles:    []string{roleTeacher},
		}); err != nil {
			s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "교사용 테스트 고객 프로필을 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
			return
		}
	}

	user := authUser{
		ID:        account.ID,
		Login:     account.LoginID,
		Name:      account.DisplayName,
		AccountID: account.ID,
		BoothID:   account.BoothID,
		Provider:  "internal",
		Roles:     []string{normalizeInternalRole(account.Role)},
	}
	session := authSession{Token: randomToken(), User: user, Role: normalizeInternalRole(account.Role), ExpiresAt: time.Now().Add(envDuration("INTERNAL_SESSION_TTL", 12*time.Hour))}
	if err := s.store.saveSession(r.Context(), session); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "세션을 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	setSessionCookie(w, session)
	s.created(w, r, map[string]any{
		"accessToken": session.Token,
		"tokenType":   "Bearer",
		"expiresAt":   session.ExpiresAt.Format(time.RFC3339),
		"user":        session.User,
		"role":        session.Role,
		"account":     sanitizeInternalAccount(account),
	})
}

func (s *server) ensureBootstrapAccounts(ctx context.Context) error {
	adminLogin := env("BOOTSTRAP_ADMIN_LOGIN", "")
	adminPassword := env("BOOTSTRAP_ADMIN_PASSWORD", "")
	if (adminLogin == "") != (adminPassword == "") {
		return errors.New("BOOTSTRAP_ADMIN_LOGIN and BOOTSTRAP_ADMIN_PASSWORD must be set together")
	}
	if adminLogin != "" {
		if _, found, err := s.store.internalAccountByLogin(ctx, adminLogin); err != nil {
			return err
		} else if !found {
			if _, err := s.createInternalAccount(ctx, internalAccountInput{LoginID: adminLogin, Password: adminPassword, Role: roleAdmin, DisplayName: "Bootstrap Admin", ForcePasswordChange: true, CreatedBy: "bootstrap"}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *server) createInternalAccount(ctx context.Context, input internalAccountInput) (internalAccount, error) {
	return s.adminAccounts().Create(ctx, input)
}

func (s *server) ensureTeacherCustomerProfile(ctx context.Context, user authUser) error {
	user.Name = envDefault(user.Name, user.Login)
	_, err := s.store.saveCustomerProfile(ctx, user, map[string]any{
		"name":       user.Name,
		"schoolName": "대마고등학교",
		"studentNo":  teacherStudentNo(user.ID),
	})
	return err
}

func (s *server) sessionFromRequest(r *http.Request) (authSession, bool) {
	if session, ok := authSessionFromContext(r.Context()); ok {
		return session, true
	}
	token := bearerToken(r)
	if token == "" {
		if cookie, err := r.Cookie("daema_session"); err == nil {
			token = cookie.Value
		}
	}
	if token == "" {
		return authSession{}, false
	}
	session, ok, err := s.store.session(r.Context(), token)
	if err != nil {
		slog.Error("load auth session", "error", err)
		return authSession{}, false
	}
	return session, ok
}

func setSessionCookie(w http.ResponseWriter, session authSession) {
	http.SetCookie(w, sessionCookie(session.Token, session.ExpiresAt, 0))
}

func sessionCookie(value string, expires time.Time, maxAge int) *http.Cookie {
	cookie := &http.Cookie{
		Name:     "daema_session",
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
	}
	if domain := env("SESSION_COOKIE_DOMAIN", ""); domain != "" {
		cookie.Domain = domain
	}
	return cookie
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return ""
	}
	return strings.TrimSpace(header[len("Bearer "):])
}

func (s *server) authzMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasPrefix(path, "/api/admin/"):
			session, ok := s.requireRole(w, r, roleAdmin)
			if !ok {
				return
			}
			r = r.WithContext(contextWithAuthSession(r.Context(), session))
		case strings.HasPrefix(path, "/api/seller/"):
			session, ok := s.requireRole(w, r, roleBooth)
			if !ok {
				return
			}
			if !s.requireBoothScope(w, r, session) {
				return
			}
			r = r.WithContext(contextWithAuthSession(r.Context(), session))
		case strings.HasPrefix(path, "/api/customer/"):
			session, ok := s.requireAnyRole(w, r, roleCustomer, roleTeacher)
			if !ok {
				return
			}
			if sessionHasRole(session, roleTeacher) {
				if err := s.ensureTeacherCustomerProfile(r.Context(), session.User); err != nil {
					s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "교사용 테스트 고객 프로필을 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
					return
				}
			}
			r = r.WithContext(contextWithAuthSession(r.Context(), session))
		case path == "/api/files/uploads":
			session, ok := s.requireAnyRole(w, r, roleAdmin, roleBooth)
			if !ok {
				return
			}
			r = r.WithContext(contextWithAuthSession(r.Context(), session))
		}
		next.ServeHTTP(w, r)
	})
}

func contextWithAuthSession(ctx context.Context, session authSession) context.Context {
	return context.WithValue(ctx, authSessionContextKey, session)
}

func authSessionFromContext(ctx context.Context) (authSession, bool) {
	session, ok := ctx.Value(authSessionContextKey).(authSession)
	return session, ok
}

func (s *server) requireBoothScope(w http.ResponseWriter, r *http.Request, session authSession) bool {
	boothID := boothIDFromSellerPath(r.URL.Path)
	if boothID == "" {
		return true
	}
	if session.User.BoothID == "" || session.User.BoothID != boothID {
		s.fail(w, r, http.StatusForbidden, "BOOTH_SCOPE_REQUIRED", "해당 부스에 접근할 권한이 없습니다.", map[string]any{"boothId": boothID})
		return false
	}
	return true
}

func boothIDFromSellerPath(path string) string {
	const prefix = "/api/seller/booths/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(rest, "/")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

func (s *server) requireRole(w http.ResponseWriter, r *http.Request, role string) (authSession, bool) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "로그인이 필요합니다.", nil)
		return authSession{}, false
	}
	if !sessionHasRole(session, role) {
		code := "FORBIDDEN"
		message := "요청한 리소스에 접근할 권한이 없습니다."
		if role == roleAdmin {
			code = "ADMIN_ROLE_REQUIRED"
			message = "관리자 권한이 필요합니다."
		}
		if role == roleBooth {
			code = "BOOTH_ROLE_REQUIRED"
			message = "부스 계정 권한이 필요합니다."
		}
		s.fail(w, r, http.StatusForbidden, code, message, nil)
		return authSession{}, false
	}
	return session, true
}

func (s *server) requireAnyRole(w http.ResponseWriter, r *http.Request, roles ...string) (authSession, bool) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "로그인이 필요합니다.", nil)
		return authSession{}, false
	}
	for _, role := range roles {
		if sessionHasRole(session, role) {
			return session, true
		}
	}
	s.fail(w, r, http.StatusForbidden, "FORBIDDEN", "요청한 리소스에 접근할 권한이 없습니다.", nil)
	return authSession{}, false
}

func sessionHasRole(session authSession, role string) bool {
	return authUserHasRole(session.User, role)
}

func authUserHasRole(user authUser, role string) bool {
	for _, item := range user.Roles {
		if item == role {
			return true
		}
		if role == roleBooth && item == "seller" {
			return true
		}
	}
	return false
}

func normalizeInternalRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case roleAdmin:
		return roleAdmin
	case roleTeacher:
		return roleTeacher
	case roleBooth, "seller", "booth_owner", "booth_staff":
		return roleBooth
	default:
		return roleBooth
	}
}

func rolesForGitHubUser(requestedRole, email, login string) []string {
	_, _, _ = requestedRole, email, login
	return []string{roleCustomer}
}

func listContainsEnv(key, value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	for _, item := range strings.Split(env(key, ""), ",") {
		if strings.ToLower(strings.TrimSpace(item)) == value {
			return true
		}
	}
	return false
}

func appendQuery(raw string, values map[string]string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	for key, value := range values {
		q.Set(key, value)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func randomToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

func sessionTokenHashID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "session-" + hex.EncodeToString(sum[:])
}

func internalAccountID(loginID string) string {
	normalized := strings.ToLower(strings.TrimSpace(loginID))
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_", "@", "_at_", ".", "_")
	return "account-" + replacer.Replace(normalized)
}

func teacherStudentNo(accountID string) string {
	sum := sha256.Sum256([]byte("teacher-customer:" + accountID))
	value := int64(sum[0])<<32 | int64(sum[1])<<24 | int64(sum[2])<<16 | int64(sum[3])<<8 | int64(sum[4])
	return fmt.Sprintf("9%011d", value%100_000_000_000)
}

func hashPassword(password string) (string, error) {
	raw, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func checkPassword(hash, password string) bool {
	if hash == "" || password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func sanitizeInternalAccount(account internalAccount) map[string]any {
	data, err := mapFromStruct(account)
	if err != nil {
		return map[string]any{"id": account.ID, "loginId": account.LoginID, "role": account.Role, "status": account.Status}
	}
	delete(data, "passwordHash")
	return data
}
