package server

type walletLedgerRequest struct {
	User      authUser
	ID        string
	Type      string
	Direction string
	Currency  string
	Amount    int
	Extras    map[string]any
}

func newWalletLedgerRequest(user authUser, id, txType, direction, currency string, value int, extras map[string]any) walletLedgerRequest {
	return walletLedgerRequest{
		User:      user,
		ID:        id,
		Type:      txType,
		Direction: direction,
		Currency:  currency,
		Amount:    value,
		Extras:    cloneMap(extras),
	}
}
