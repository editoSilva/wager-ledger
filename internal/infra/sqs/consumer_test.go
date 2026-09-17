package sqsinfra

import "testing"

func TestDecodeEnvelope(t *testing.T) {
	body := `{"messageId":"msg-1","type":"WagerTransactionRequested","data":{"providerId":"provider-a","externalTransactionId":"tx-1","idempotencyKey":"key-1","playerId":"player-1","walletId":"wallet-1","roundId":"round-1","gameId":"game-1","kind":"BET","money":{"amount":"1.00","currency":"BRL"}}}`
	e, hash, err := decodeEnvelope(&body)
	if err != nil {
		t.Fatalf("decodeEnvelope: %v", err)
	}
	if e.MessageID != "msg-1" || hash == "" {
		t.Fatalf("envelope/hash inválidos: %#v %q", e, hash)
	}
}
