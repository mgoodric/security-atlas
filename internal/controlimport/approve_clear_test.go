package controlimport

import (
	"context"
	"errors"
	"testing"
)

// The pre-transaction guards refuse before any database call, so a Store
// with no pool is enough.
func TestApproveClearGuards(t *testing.T) {
	t.Parallel()
	s := &Store{}
	if _, err := s.ApproveClear(context.Background(), ApproveClearInput{BatchID: "6f1c2c8e-0000-4000-8000-000000000001", Approver: " "}); !errors.Is(err, ErrApproverRequired) {
		t.Errorf("blank approver: err = %v, want ErrApproverRequired", err)
	}
	if _, err := s.ApproveClear(context.Background(), ApproveClearInput{BatchID: "not-a-uuid", Approver: "user-1"}); !errors.Is(err, ErrBatchNotFound) {
		t.Errorf("bad batch id: err = %v, want ErrBatchNotFound", err)
	}
}
