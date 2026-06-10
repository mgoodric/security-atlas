package eventgrid

import (
	"net/http"
	"testing"
)

const testDeliveryKey = "test-delivery-key-not-a-real-secret"

func TestDeliveryKeyVerifier_Header(t *testing.T) {
	t.Parallel()
	v := NewDeliveryKeyVerifier(CredentialHeader, "Authorization", testDeliveryKey)

	good := http.Header{}
	good.Set("Authorization", testDeliveryKey)
	if err := v.Verify(nil, good); err != nil {
		t.Fatalf("matching header rejected: %v", err)
	}

	bad := http.Header{}
	bad.Set("Authorization", "test-wrong-key")
	if err := v.Verify(nil, bad); err == nil {
		t.Fatal("wrong header accepted")
	}

	if err := v.Verify(nil, http.Header{}); err == nil {
		t.Fatal("missing header accepted")
	}
}

func TestDeliveryKeyVerifier_QueryLocationFailsClosedViaHeaderPath(t *testing.T) {
	t.Parallel()
	// A query-located verifier called via the header-only Verify must fail closed
	// (the receiver routes query verification through verifyValue instead).
	v := NewDeliveryKeyVerifier(CredentialQuery, "code", testDeliveryKey)
	if err := v.Verify(nil, http.Header{}); err == nil {
		t.Fatal("query verifier via header path must fail closed")
	}
}

func TestDeliveryKeyVerifier_VerifyValue(t *testing.T) {
	t.Parallel()
	v := NewDeliveryKeyVerifier(CredentialQuery, "code", testDeliveryKey)
	if err := v.verifyValue(testDeliveryKey); err != nil {
		t.Fatalf("matching value rejected: %v", err)
	}
	if err := v.verifyValue("test-wrong"); err == nil {
		t.Fatal("wrong value accepted")
	}
	if err := v.verifyValue(""); err == nil {
		t.Fatal("empty value accepted")
	}
}

func TestDeliveryKeyVerifier_Accessors(t *testing.T) {
	t.Parallel()
	v := NewDeliveryKeyVerifier(CredentialQuery, "code", testDeliveryKey)
	if v.Location() != CredentialQuery {
		t.Fatal("Location mismatch")
	}
	if v.QueryName() != "code" {
		t.Fatal("QueryName mismatch")
	}
}
