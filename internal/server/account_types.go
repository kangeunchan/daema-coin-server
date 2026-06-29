package server

type adminAccountCreateRequest struct {
	LoginID             string `json:"loginId"`
	Username            string `json:"username"`
	Login               string `json:"login"`
	Password            string `json:"password"`
	Role                string `json:"role"`
	DisplayName         string `json:"displayName"`
	BoothID             string `json:"boothId"`
	ForcePasswordChange *bool  `json:"forcePasswordChange"`
}

func (r adminAccountCreateRequest) input(createdBy string) internalAccountInput {
	forcePasswordChange := true
	if r.ForcePasswordChange != nil {
		forcePasswordChange = *r.ForcePasswordChange
	}
	return internalAccountInput{
		LoginID:             firstNonEmpty(r.LoginID, r.Username, r.Login),
		Password:            r.Password,
		Role:                r.Role,
		DisplayName:         r.DisplayName,
		BoothID:             r.BoothID,
		ForcePasswordChange: forcePasswordChange,
		CreatedBy:           createdBy,
	}
}

type adminAccountUpdateRequest struct {
	DisplayName         *string `json:"displayName"`
	Status              *string `json:"status"`
	BoothID             *string `json:"boothId"`
	ForcePasswordChange *bool   `json:"forcePasswordChange"`
}

func (r adminAccountUpdateRequest) mapPayload() map[string]any {
	out := map[string]any{}
	if r.DisplayName != nil {
		out["displayName"] = *r.DisplayName
	}
	if r.Status != nil {
		out["status"] = *r.Status
	}
	if r.BoothID != nil {
		out["boothId"] = *r.BoothID
	}
	if r.ForcePasswordChange != nil {
		out["forcePasswordChange"] = *r.ForcePasswordChange
	}
	return out
}

type adminAccountResetPasswordRequest struct {
	Password string `json:"password"`
}
