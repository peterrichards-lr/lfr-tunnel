package db

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupTestDB(t *testing.T) (*DB, string) {
	// Create a temporary file for SQLite DB to test proper file persistence and concurrency
	tmpDir, err := os.MkdirTemp("", "lfr-tunnel-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := Open(dbPath)
	if err != nil {
		os.RemoveAll(tmpDir) //nolint:errcheck
		t.Fatalf("failed to open test database: %v", err)
	}

	return database, tmpDir
}

func cleanupTestDB(database *DB, tmpDir string) {
	database.Close()     //nolint:errcheck
	os.RemoveAll(tmpDir) //nolint:errcheck
}

func TestUserCRUD(t *testing.T) {
	database, tmpDir := setupTestDB(t)
	defer cleanupTestDB(database, tmpDir)

	user := &User{
		ID:            "user-1",
		Email:         "user1@liferay.com",
		FirstName:     "John",
		LastName:      "Doe",
		Role:          "user",
		Status:        "pending",
		ApprovalToken: "dummy_approval_token",
		ClaimToken:    "dummy_claim_token",
	}

	// 1. Create User
	if err := database.CreateUser(user); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Double-insert should fail (conflict on ID)
	if err := database.CreateUser(user); err == nil {
		t.Error("expected duplicate user insert to fail, but it succeeded")
	}

	// 2. Get User
	fetched, err := database.GetUser("user-1")
	if err != nil {
		t.Fatalf("failed to fetch user: %v", err)
	}
	if fetched.Email != user.Email || fetched.FirstName != user.FirstName {
		t.Errorf("fetched user mismatch: got email %s, name %s", fetched.Email, fetched.FirstName)
	}
	if fetched.ApprovalToken != "dummy_approval_token" || fetched.ClaimToken != "dummy_claim_token" {
		t.Errorf("fetched user tokens mismatch: got approval=%s, claim=%s", fetched.ApprovalToken, fetched.ClaimToken)
	}

	// Fetch by email
	fetchedEmail, err := database.GetUserByEmail("user1@liferay.com")
	if err != nil {
		t.Fatalf("failed to fetch user by email: %v", err)
	}
	if fetchedEmail.ID != user.ID {
		t.Errorf("fetched user email mismatch: got ID %s", fetchedEmail.ID)
	}

	// Fetch by approval token
	fetchedApp, err := database.GetUserByApprovalToken("dummy_approval_token")
	if err != nil {
		t.Fatalf("failed to fetch user by approval token: %v", err)
	}
	if fetchedApp.ID != user.ID {
		t.Errorf("fetched user approval token mismatch: got ID %s", fetchedApp.ID)
	}

	// Fetch by claim token
	fetchedClaim, err := database.GetUserByClaimToken("dummy_claim_token")
	if err != nil {
		t.Fatalf("failed to fetch user by claim token: %v", err)
	}
	if fetchedClaim.ID != user.ID {
		t.Errorf("fetched user claim token mismatch: got ID %s", fetchedClaim.ID)
	}

	// Get non-existent user
	_, err = database.GetUser("non-existent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound for non-existent user, got %v", err)
	}

	// 3. Update User
	fetched.FirstName = "Johnny"
	fetched.Status = "approved"
	fetched.ApprovalToken = ""
	fetched.ClaimToken = "dummy_new_claim_token"
	if err := database.UpdateUser(fetched); err != nil {
		t.Fatalf("failed to update user: %v", err)
	}

	updated, err := database.GetUser("user-1")
	if err != nil {
		t.Fatalf("failed to fetch updated user: %v", err)
	}
	if updated.FirstName != "Johnny" || updated.Status != "approved" {
		t.Errorf("update was not saved: got name %s, status %s", updated.FirstName, updated.Status)
	}
	if updated.ApprovalToken != "" || updated.ClaimToken != "dummy_new_claim_token" {
		t.Errorf("update tokens were not saved: got approval=%q, claim=%q", updated.ApprovalToken, updated.ClaimToken)
	}

	// Update non-existent user should fail
	nonExistent := &User{ID: "non-existent", Email: "no@liferay.com"}
	if err := database.UpdateUser(nonExistent); err != ErrNotFound {
		t.Errorf("expected ErrNotFound updating non-existent user, got %v", err)
	}

	// 4. List Users
	users, err := database.ListUsers()
	if err != nil {
		t.Fatalf("failed to list users: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("expected 1 user in database, got %d", len(users))
	}
}

func TestPATCRUD(t *testing.T) {
	database, tmpDir := setupTestDB(t)
	defer cleanupTestDB(database, tmpDir)

	// Create user first
	user := &User{
		ID:     "dev-1",
		Email:  "dev1@liferay.com",
		Role:   "user",
		Status: "approved",
	}
	if err := database.CreateUser(user); err != nil {
		t.Fatalf("failed to setup test user: %v", err)
	}

	expiresAt := time.Now().Add(24 * time.Hour).UTC()
	pat := &PersonalAccessToken{
		UserID:      "dev-1",
		TokenHash:   "my-sha-256-hash-value-12345",
		TokenPrefix: "lfr_pat_my",
		Name:        "My Macbook Pro",
		ExpiresAt:   &expiresAt,
	}

	// 1. Create PAT
	if err := database.CreatePAT(pat); err != nil {
		t.Fatalf("failed to create PAT: %v", err)
	}
	if pat.ID == 0 {
		t.Error("expected non-zero inserted ID on PAT")
	}

	// 2. Fetch PAT by Hash
	fetched, err := database.GetPATByHash("my-sha-256-hash-value-12345")
	if err != nil {
		t.Fatalf("failed to fetch PAT by hash: %v", err)
	}
	if fetched.Name != pat.Name || fetched.TokenPrefix != pat.TokenPrefix {
		t.Errorf("fetched PAT mismatch: got name %s, prefix %s", fetched.Name, fetched.TokenPrefix)
	}
	if fetched.ExpiresAt == nil || fetched.ExpiresAt.Format(time.RFC3339) != expiresAt.Format(time.RFC3339) {
		t.Errorf("fetched PAT expires_at mismatch: got %v", fetched.ExpiresAt)
	}
	if fetched.RevokedAt != nil || fetched.LastUsedAt != nil {
		t.Errorf("expected new PAT to have nil revoked_at and last_used_at, got revoked=%v, last_used=%v", fetched.RevokedAt, fetched.LastUsedAt)
	}

	// Fetch non-existent PAT
	_, err = database.GetPATByHash("non-existent-hash")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound for non-existent PAT hash, got %v", err)
	}

	// 3. List PATs
	pats, err := database.ListPATs("dev-1")
	if err != nil {
		t.Fatalf("failed to list PATs: %v", err)
	}
	if len(pats) != 1 {
		t.Errorf("expected 1 PAT for user dev-1, got %d", len(pats))
	}

	// 4. Update PAT last used
	if err := database.UpdatePATUsed(pat.ID); err != nil {
		t.Fatalf("failed to update last used: %v", err)
	}
	updatedPAT, err := database.GetPATByHash("my-sha-256-hash-value-12345")
	if err != nil {
		t.Fatalf("failed to fetch PAT: %v", err)
	}
	if updatedPAT.LastUsedAt == nil {
		t.Error("expected non-nil last_used_at after update")
	}

	// 5. Revoke PAT
	if err := database.RevokePAT(pat.ID); err != nil {
		t.Fatalf("failed to revoke PAT: %v", err)
	}
	revokedPAT, err := database.GetPATByHash("my-sha-256-hash-value-12345")
	if err != nil {
		t.Fatalf("failed to fetch PAT: %v", err)
	}
	if revokedPAT.RevokedAt == nil {
		t.Error("expected non-nil revoked_at after revocation")
	}
}

func TestPATForeignKeysAndCascade(t *testing.T) {
	database, tmpDir := setupTestDB(t)
	defer cleanupTestDB(database, tmpDir)

	// Create PAT without existing user should fail (foreign key constraint)
	pat := &PersonalAccessToken{
		UserID:      "non-existent-user-id",
		TokenHash:   "hash-without-user",
		TokenPrefix: "lfr_pat_no",
		Name:        "Orphan Token",
	}
	err := database.CreatePAT(pat)
	if err == nil {
		t.Error("expected foreign key constraint error when creating PAT for non-existent user, but got no error")
	}

	// Create user
	user := &User{
		ID:     "user-to-delete",
		Email:  "deleteme@liferay.com",
		Role:   "user",
		Status: "approved",
	}
	if err := database.CreateUser(user); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Create PAT for user
	pat.UserID = user.ID
	if err := database.CreatePAT(pat); err != nil {
		t.Fatalf("failed to create PAT: %v", err)
	}

	// Delete user directly using database SQL (since we don't have DeleteUser method)
	_, err = database.conn.Exec("DELETE FROM users WHERE id = ?", user.ID)
	if err != nil {
		t.Fatalf("failed to delete user: %v", err)
	}

	// Verify PAT is deleted (cascade delete check)
	_, err = database.GetPATByHash(pat.TokenHash)
	if err != ErrNotFound {
		t.Errorf("expected PAT to be deleted via CASCADE, but got err: %v", err)
	}
}

func TestAuditLogWriteAndFilter(t *testing.T) {
	database, tmpDir := setupTestDB(t)
	defer cleanupTestDB(database, tmpDir)

	e1 := &AuditEntry{
		ActorID:    "admin@test.com",
		Action:     "user.role_changed",
		TargetType: "user",
		TargetID:   "dev@test.com",
		Details:    `{"role":"admin"}`,
		IPAddress:  "127.0.0.1",
	}

	if err := database.WriteAuditEntry(e1); err != nil {
		t.Fatalf("failed to write audit entry: %v", err)
	}
	if e1.ID == 0 {
		t.Error("expected non-zero ID for audit entry")
	}

	e2 := &AuditEntry{
		ActorID:    "admin@test.com",
		Action:     "token.revoked",
		TargetType: "token",
		TargetID:   "123",
	}
	_ = database.WriteAuditEntry(e2)

	e3 := &AuditEntry{
		ActorID:    "other@test.com",
		Action:     "token.revoked",
		TargetType: "token",
		TargetID:   "456",
	}
	_ = database.WriteAuditEntry(e3)

	// Filter by Actor
	entries, err := database.ListAuditEntries(AuditFilter{ActorID: "admin@test.com"})
	if err != nil {
		t.Fatalf("failed to list audit entries: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries for actor admin@test.com, got %d", len(entries))
	}

	// Filter by Action
	entries, err = database.ListAuditEntries(AuditFilter{Action: "token.revoked"})
	if err != nil {
		t.Fatalf("failed to list audit entries: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries for action token.revoked, got %d", len(entries))
	}

	// Filter by Target
	entries, err = database.ListAuditEntries(AuditFilter{TargetID: "dev@test.com"})
	if err != nil {
		t.Fatalf("failed to list audit entries: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry for target dev@test.com, got %d", len(entries))
	}
	if entries[0].Details != `{"role":"admin"}` {
		t.Errorf("expected details to be preserved, got %s", entries[0].Details)
	}
}

func TestListAllPATsAndCountAdmins(t *testing.T) {
	database, tmpDir := setupTestDB(t)
	defer cleanupTestDB(database, tmpDir)

	u1 := &User{ID: "u1", Email: "admin1@test.com", Role: "admin", Status: "approved"}
	u2 := &User{ID: "u2", Email: "admin2@test.com", Role: "admin", Status: "pending"}
	u3 := &User{ID: "u3", Email: "user@test.com", Role: "user", Status: "approved"}

	_ = database.CreateUser(u1)
	_ = database.CreateUser(u2)
	_ = database.CreateUser(u3)

	count, err := database.CountAdmins()
	if err != nil {
		t.Fatalf("failed to count admins: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 approved admin, got %d", count)
	}

	p1 := &PersonalAccessToken{UserID: "u1", TokenHash: "h1", TokenPrefix: "p1", Name: "n1"}
	p2 := &PersonalAccessToken{UserID: "u3", TokenHash: "h2", TokenPrefix: "p2", Name: "n2"}
	_ = database.CreatePAT(p1)
	_ = database.CreatePAT(p2)

	pats, err := database.ListAllPATs()
	if err != nil {
		t.Fatalf("failed to list all pats: %v", err)
	}
	if len(pats) != 2 {
		t.Errorf("expected 2 total pats, got %d", len(pats))
	}
}
