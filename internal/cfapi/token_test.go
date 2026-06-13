package cfapi

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func makeToken(t *testing.T, enc *base64.Encoding, a, tid, s string) string {
	t.Helper()
	b, err := json.Marshal(rawClaims{A: a, T: tid, S: s})
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return enc.EncodeToString(b)
}

func TestDecodeTunnelToken_StdAndVariants(t *testing.T) {
	const acc = "0123456789abcdef0123456789abcdef"
	const tid = "f70ff985-a4ef-4643-bbbc-4a0ed4fc8415"
	for name, enc := range map[string]*base64.Encoding{
		"std":    base64.StdEncoding,
		"rawstd": base64.RawStdEncoding,
		"url":    base64.URLEncoding,
		"rawurl": base64.RawURLEncoding,
	} {
		tok := makeToken(t, enc, acc, tid, "c2VjcmV0")
		got, err := DecodeTunnelToken(tok)
		if err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
		if got.AccountTag != acc || got.TunnelID != tid {
			t.Fatalf("%s: got %+v, want acc=%s tid=%s", name, got, acc, tid)
		}
	}
}

func TestDecodeTunnelToken_TrimsWhitespace(t *testing.T) {
	tok := "  " + makeToken(t, base64.StdEncoding, "acc", "tid", "s") + "\n"
	got, err := DecodeTunnelToken(tok)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.AccountTag != "acc" || got.TunnelID != "tid" {
		t.Fatalf("got %+v", got)
	}
}

func TestDecodeTunnelToken_Errors(t *testing.T) {
	cases := map[string]string{
		"empty":      "",
		"not base64": "!!!not base64!!!",
		"missing t":  base64.StdEncoding.EncodeToString([]byte(`{"a":"acc"}`)),
		"missing a":  base64.StdEncoding.EncodeToString([]byte(`{"t":"tid"}`)),
	}
	for name, in := range cases {
		if _, err := DecodeTunnelToken(in); err == nil {
			t.Fatalf("%s: expected error, got nil", name)
		}
	}
}
