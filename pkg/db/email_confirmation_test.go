package db

import "testing"

// #2472: a confirmed address is remembered per account, and goes with the account when it is
// erased.
func TestEmailConfirmationIsRecordedAndErasedWithTheUser(t *testing.T) {
	database := setupTestDB(t)
	if err := database.CreateUser(&User{ID: "a@example.com", Email: "a@example.com", Role: "user", Status: UserStatusApproved}); err != nil {
		t.Fatal(err)
	}
	if ok, err := database.EmailConfirmed("a@example.com"); err != nil || ok {
		t.Fatalf("before recording: %v, %v", ok, err)
	}
	for i := 0; i < 2; i++ { // a second confirmation is not an error
		if err := database.RecordEmailConfirmed("a@example.com", "sso_link"); err != nil {
			t.Fatalf("recording %d: %v", i, err)
		}
	}
	if ok, err := database.EmailConfirmed("a@example.com"); err != nil || !ok {
		t.Fatalf("after recording: %v, %v", ok, err)
	}
	if err := database.DeleteUser("a@example.com"); err != nil {
		t.Fatal(err)
	}
	if ok, err := database.EmailConfirmed("a@example.com"); err != nil || ok {
		t.Errorf("the confirmation outlived the account: %v, %v", ok, err)
	}
}
