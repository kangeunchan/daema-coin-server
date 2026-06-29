package server

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	errAccountNotFound       = errors.New("account not found")
	errAccountAlreadyExists  = errors.New("account already exists")
	errInvalidAccountStatus  = errors.New("invalid account status")
	errInvalidAccountLogin   = errors.New("loginId is required")
	errInvalidAccountPass    = errors.New("password must be at least 10 characters")
	errBoothAccountMissingID = errors.New("boothId is required for booth accounts")
)

type adminAccountService struct {
	store adminAccountStore
}

func (s *server) adminAccounts() adminAccountService {
	return adminAccountService{store: s.store}
}

type adminAccountStore interface {
	internalAccount(ctx context.Context, id string) (internalAccount, bool, error)
	internalAccountByLogin(ctx context.Context, loginID string) (internalAccount, bool, error)
	saveInternalAccount(ctx context.Context, account internalAccount) (bool, error)
}

func (svc adminAccountService) Create(ctx context.Context, input internalAccountInput) (internalAccount, error) {
	loginID := strings.TrimSpace(input.LoginID)
	if loginID == "" {
		return internalAccount{}, errInvalidAccountLogin
	}
	if len(input.Password) < 10 {
		return internalAccount{}, errInvalidAccountPass
	}
	role := normalizeInternalRole(input.Role)
	if role == roleBooth && strings.TrimSpace(input.BoothID) == "" {
		return internalAccount{}, errBoothAccountMissingID
	}
	if _, found, err := svc.store.internalAccountByLogin(ctx, loginID); err != nil {
		return internalAccount{}, err
	} else if found {
		return internalAccount{}, errAccountAlreadyExists
	}
	passwordHash, err := hashPassword(input.Password)
	if err != nil {
		return internalAccount{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	account := internalAccount{
		ID:                  internalAccountID(loginID),
		LoginID:             loginID,
		PasswordHash:        passwordHash,
		Role:                role,
		Status:              "active",
		DisplayName:         strings.TrimSpace(input.DisplayName),
		BoothID:             strings.TrimSpace(input.BoothID),
		ForcePasswordChange: input.ForcePasswordChange,
		CreatedBy:           input.CreatedBy,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	created, err := svc.store.saveInternalAccount(ctx, account)
	if err != nil {
		return internalAccount{}, err
	}
	if !created {
		return internalAccount{}, errAccountAlreadyExists
	}
	return account, nil
}

func (svc adminAccountService) Update(ctx context.Context, accountID string, body map[string]any) (internalAccount, error) {
	account, found, err := svc.store.internalAccount(ctx, accountID)
	if err != nil {
		return internalAccount{}, err
	}
	if !found {
		return internalAccount{}, errAccountNotFound
	}
	if value := strings.TrimSpace(stringValue(body["displayName"])); value != "" {
		account.DisplayName = value
	}
	if value := strings.TrimSpace(stringValue(body["status"])); value != "" {
		switch value {
		case "active", "disabled", "locked":
			account.Status = value
		default:
			return internalAccount{}, errInvalidAccountStatus
		}
	}
	if value := strings.TrimSpace(stringValue(body["boothId"])); value != "" {
		account.BoothID = value
	}
	if value, ok := body["forcePasswordChange"]; ok {
		account.ForcePasswordChange = boolValue(value, account.ForcePasswordChange)
	}
	account.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if _, err := svc.store.saveInternalAccount(ctx, account); err != nil {
		return internalAccount{}, err
	}
	return account, nil
}

func (svc adminAccountService) ResetPassword(ctx context.Context, accountID, password string) (internalAccount, error) {
	account, found, err := svc.store.internalAccount(ctx, accountID)
	if err != nil {
		return internalAccount{}, err
	}
	if !found {
		return internalAccount{}, errAccountNotFound
	}
	if len(password) < 10 {
		return internalAccount{}, errInvalidAccountPass
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return internalAccount{}, err
	}
	account.PasswordHash = passwordHash
	account.ForcePasswordChange = true
	account.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if _, err := svc.store.saveInternalAccount(ctx, account); err != nil {
		return internalAccount{}, err
	}
	return account, nil
}
