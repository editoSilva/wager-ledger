package usecase

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type canonicalWagerFields struct {
	ProviderID                     string
	ExternalTransactionID          string
	PlayerID                       string
	WalletID                       string
	RoundID                        string
	GameID                         string
	Kind                           string
	MoneyAmount                    string
	MoneyCurrency                  string
	ReferenceExternalTransactionID string
}

func computePayloadHash(f canonicalWagerFields) (string, error) {
	canonical := map[string]any{
		"providerId":                     f.ProviderID,
		"externalTransactionId":          f.ExternalTransactionID,
		"playerId":                       f.PlayerID,
		"walletId":                       f.WalletID,
		"roundId":                        f.RoundID,
		"gameId":                         f.GameID,
		"kind":                           f.Kind,
		"money":                          map[string]string{"amount": f.MoneyAmount, "currency": f.MoneyCurrency},
		"referenceExternalTransactionId": f.ReferenceExternalTransactionID,
	}

	b, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
