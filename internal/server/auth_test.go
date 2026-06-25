package server

import "testing"

func TestGitHubRolesAreCustomerOnly(t *testing.T) {
	roles := rolesForGitHubUser("admin", "admin@example.com", "admin-login")
	if len(roles) != 1 || roles[0] != roleCustomer {
		t.Fatalf("GitHub roles = %#v, want customer only", roles)
	}
}

func TestSessionTokenHashIDDoesNotExposeToken(t *testing.T) {
	token := "plain-session-token"
	id := sessionTokenHashID(token)
	if id == token {
		t.Fatal("session token hash id must not equal the raw token")
	}
	if id != sessionTokenHashID(token) {
		t.Fatal("session token hash id must be deterministic")
	}
}

func TestInternalAccountSanitizeRemovesPasswordHash(t *testing.T) {
	account := internalAccount{
		ID:           "account-admin",
		LoginID:      "admin",
		PasswordHash: "hashed-password",
		Role:         roleAdmin,
		Status:       "active",
	}
	data := sanitizeInternalAccount(account)
	if _, ok := data["passwordHash"]; ok {
		t.Fatal("sanitized account leaked passwordHash")
	}
	if data["loginId"] != "admin" {
		t.Fatalf("loginId = %v, want admin", data["loginId"])
	}
}

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := hashPassword("long-enough-password")
	if err != nil {
		t.Fatalf("hashPassword failed: %v", err)
	}
	if !checkPassword(hash, "long-enough-password") {
		t.Fatal("checkPassword rejected the original password")
	}
	if checkPassword(hash, "wrong-password") {
		t.Fatal("checkPassword accepted the wrong password")
	}
}

func TestNormalizeInternalRole(t *testing.T) {
	cases := map[string]string{
		"admin":       roleAdmin,
		"booth":       roleBooth,
		"seller":      roleBooth,
		"booth_owner": roleBooth,
		"":            roleBooth,
	}
	for input, want := range cases {
		if got := normalizeInternalRole(input); got != want {
			t.Fatalf("normalizeInternalRole(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestBoothIDFromSellerPath(t *testing.T) {
	cases := map[string]string{
		"/api/seller/booths/booth-1":               "booth-1",
		"/api/seller/booths/booth-1/products":      "booth-1",
		"/api/seller/booths/booth-1/reports/sales": "booth-1",
		"/api/seller/products/product-1":           "",
		"/api/customer/booths/booth-1":             "",
		"/api/seller/booths":                       "",
		"/api/seller/booths/":                      "",
	}
	for input, want := range cases {
		if got := boothIDFromSellerPath(input); got != want {
			t.Fatalf("boothIDFromSellerPath(%q) = %q, want %q", input, got, want)
		}
	}
}
