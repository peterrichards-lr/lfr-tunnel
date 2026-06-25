package db

import (
	"errors"
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

func TestPATRetentionPruning(t *testing.T) {
	database, tmpDir := setupTestDB(t)
	defer cleanupTestDB(database, tmpDir)

	// Create user
	user := &User{
		ID:     "ret-user",
		Email:  "retention@liferay.com",
		Role:   "user",
		Status: "approved",
	}
	if err := database.CreateUser(user); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	now := time.Now().UTC()
	oldTime := now.AddDate(0, 0, -35)    // 35 days ago
	recentTime := now.AddDate(0, 0, -10) // 10 days ago

	// 1. Old expired token (should be pruned)
	p1 := &PersonalAccessToken{
		UserID:      user.ID,
		TokenHash:   "hash-old-expired",
		TokenPrefix: "prefix1",
		Name:        "Old Expired",
		ExpiresAt:   &oldTime,
	}
	_ = database.CreatePAT(p1)

	// 2. Recent expired token (should be kept)
	p2 := &PersonalAccessToken{
		UserID:      user.ID,
		TokenHash:   "hash-recent-expired",
		TokenPrefix: "prefix2",
		Name:        "Recent Expired",
		ExpiresAt:   &recentTime,
	}
	_ = database.CreatePAT(p2)

	// 3. Old revoked token (should be pruned)
	p3 := &PersonalAccessToken{
		UserID:      user.ID,
		TokenHash:   "hash-old-revoked",
		TokenPrefix: "prefix3",
		Name:        "Old Revoked",
		RevokedAt:   &oldTime,
	}
	_ = database.CreatePAT(p3)

	// 4. Recent revoked token (should be kept)
	p4 := &PersonalAccessToken{
		UserID:      user.ID,
		TokenHash:   "hash-recent-revoked",
		TokenPrefix: "prefix4",
		Name:        "Recent Revoked",
		RevokedAt:   &recentTime,
	}
	_ = database.CreatePAT(p4)

	// 5. Active token (should be kept)
	p5 := &PersonalAccessToken{
		UserID:      user.ID,
		TokenHash:   "hash-active",
		TokenPrefix: "prefix5",
		Name:        "Active",
	}
	_ = database.CreatePAT(p5)

	// Run retention pruning (30 days retention)
	if err := database.PruneExpiredOrRevokedPATs(30); err != nil {
		t.Fatalf("pruning failed: %v", err)
	}

	// Verify p1 is deleted
	_, err := database.GetPATByHash("hash-old-expired")
	if err != ErrNotFound {
		t.Errorf("expected p1 to be pruned, got err: %v", err)
	}

	// Verify p2 is kept
	_, err = database.GetPATByHash("hash-recent-expired")
	if err != nil {
		t.Errorf("expected p2 to be kept, got err: %v", err)
	}

	// Verify p3 is deleted
	_, err = database.GetPATByHash("hash-old-revoked")
	if err != ErrNotFound {
		t.Errorf("expected p3 to be pruned, got err: %v", err)
	}

	// Verify p4 is kept
	_, err = database.GetPATByHash("hash-recent-revoked")
	if err != nil {
		t.Errorf("expected p4 to be kept, got err: %v", err)
	}

	// Verify p5 is kept
	_, err = database.GetPATByHash("hash-active")
	if err != nil {
		t.Errorf("expected p5 to be kept, got err: %v", err)
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

func TestBlacklistCRUD(t *testing.T) {
	database, tmpDir := setupTestDB(t)
	defer cleanupTestDB(database, tmpDir)

	ip := "192.168.1.100"
	reason := "Brute-force attempt detected"

	// 1. Initially should not be blacklisted
	isBanned, err := database.IsBlacklisted(ip)
	if err != nil {
		t.Fatalf("IsBlacklisted failed: %v", err)
	}
	if isBanned {
		t.Error("IP should not be blacklisted initially")
	}

	// 2. Add to blacklist
	err = database.AddBlacklistIP(ip, reason)
	if err != nil {
		t.Fatalf("AddBlacklistIP failed: %v", err)
	}

	// 3. Should now be blacklisted
	isBanned, err = database.IsBlacklisted(ip)
	if err != nil {
		t.Fatalf("IsBlacklisted failed: %v", err)
	}
	if !isBanned {
		t.Error("IP should be blacklisted")
	}

	// 4. List blacklisted IPs
	list, err := database.ListBlacklistedIPs()
	if err != nil {
		t.Fatalf("ListBlacklistedIPs failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected exactly 1 blacklisted IP, got %d", len(list))
	}
	if list[0].IPAddress != ip {
		t.Errorf("expected IPAddress %q, got %q", ip, list[0].IPAddress)
	}
	if list[0].Reason != reason {
		t.Errorf("expected Reason %q, got %q", reason, list[0].Reason)
	}
	if list[0].CreatedAt.IsZero() {
		t.Error("expected CreatedAt timestamp to be populated, got zero time")
	}

	// 5. Remove from blacklist
	err = database.RemoveBlacklistIP(ip)
	if err != nil {
		t.Fatalf("RemoveBlacklistIP failed: %v", err)
	}

	// 6. Should no longer be blacklisted
	isBanned, err = database.IsBlacklisted(ip)
	if err != nil {
		t.Fatalf("IsBlacklisted failed: %v", err)
	}
	if isBanned {
		t.Error("IP should not be blacklisted after removal")
	}
}

func TestUserRateLimitCRUD(t *testing.T) {
	database, tmpDir := setupTestDB(t)
	defer cleanupTestDB(database, tmpDir)

	email := "ratelimit_test@example.com"
	user := &User{
		ID:                 email,
		Email:              email,
		FirstName:          "Throttle",
		LastName:           "Tester",
		Role:               "user",
		Status:             "approved",
		RateLimit:          25, // Initial rate limit quota
		LanguagePreference: "en",
	}

	// 1. Create User
	err := database.CreateUser(user)
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// 2. Fetch User and verify rate limit
	u, err := database.GetUserByEmail(email)
	if err != nil {
		t.Fatalf("GetUserByEmail failed: %v", err)
	}
	if u.RateLimit != 25 {
		t.Errorf("expected RateLimit to be 25, got %d", u.RateLimit)
	}

	// 3. Update User RateLimit
	u.RateLimit = 50
	err = database.UpdateUser(u)
	if err != nil {
		t.Fatalf("UpdateUser failed: %v", err)
	}

	// 4. Fetch again and verify updated rate limit
	u2, err := database.GetUserByEmail(email)
	if err != nil {
		t.Fatalf("GetUserByEmail failed: %v", err)
	}
	if u2.RateLimit != 50 {
		t.Errorf("expected updated RateLimit to be 50, got %d", u2.RateLimit)
	}

	// 5. List users and verify rate limit is returned
	list, err := database.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers failed: %v", err)
	}
	found := false
	for _, usr := range list {
		if usr.Email == email {
			found = true
			if usr.RateLimit != 50 {
				t.Errorf("expected ListUsers to return RateLimit 50, got %d", usr.RateLimit)
			}
			break
		}
	}
	if !found {
		t.Error("expected to find created user in ListUsers result")
	}
}

func TestSubdomainReservationCRUD(t *testing.T) {
	database, tmpDir := setupTestDB(t)
	defer cleanupTestDB(database, tmpDir)

	userID := "user-123"
	email := "reservation_user@example.com"
	maxResLimit := 5
	user := &User{
		ID:                 userID,
		Email:              email,
		FirstName:          "Reserve",
		LastName:           "Tester",
		Role:               "user",
		Status:             "approved",
		MaxReservations:    &maxResLimit,
		LanguagePreference: "en",
	}

	// 1. Create User with MaxReservations
	if err := database.CreateUser(user); err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Verify MaxReservations is fetched correctly
	u, err := database.GetUser(userID)
	if err != nil {
		t.Fatalf("GetUser failed: %v", err)
	}
	if u.MaxReservations == nil || *u.MaxReservations != 5 {
		t.Errorf("expected MaxReservations to be 5, got %v", u.MaxReservations)
	}

	// Update MaxReservations to nil (default fallback)
	u.MaxReservations = nil
	if err := database.UpdateUser(u); err != nil {
		t.Fatalf("UpdateUser failed: %v", err)
	}

	u2, err := database.GetUser(userID)
	if err != nil {
		t.Fatalf("GetUser failed: %v", err)
	}
	if u2.MaxReservations != nil {
		t.Errorf("expected MaxReservations to be nil, got %v", *u2.MaxReservations)
	}

	// 2. Create Reservations
	res1 := &SubdomainReservation{
		UserID:    userID,
		Subdomain: "custom1",
		Domain:    "lfr-demo.se",
	}
	if err := database.CreateSubdomainReservation(res1); err != nil {
		t.Fatalf("CreateSubdomainReservation failed: %v", err)
	}
	if res1.ID == 0 {
		t.Error("expected reservation ID to be populated")
	}

	// Test unique constraints (duplicate subdomain+domain should fail)
	resDup := &SubdomainReservation{
		UserID:    "other-user",
		Subdomain: "custom1",
		Domain:    "lfr-demo.se",
	}
	if err := database.CreateSubdomainReservation(resDup); err == nil {
		t.Error("expected duplicate subdomain+domain registration to fail, but it succeeded")
	}

	// 3. Fetch Reservation by Name
	fetched, err := database.GetSubdomainReservationByName("custom1", "lfr-demo.se")
	if err != nil {
		t.Fatalf("GetSubdomainReservationByName failed: %v", err)
	}
	if fetched.UserID != userID {
		t.Errorf("expected UserID to be %s, got %s", userID, fetched.UserID)
	}

	// 4. Create Expiring Reservation
	expiry := time.Now().UTC().Add(1 * time.Hour)
	res2 := &SubdomainReservation{
		UserID:    userID,
		Subdomain: "expiring1",
		Domain:    "lfr-demo.se",
		ExpiresAt: &expiry,
	}
	if err := database.CreateSubdomainReservation(res2); err != nil {
		t.Fatalf("CreateSubdomainReservation failed: %v", err)
	}

	// 5. List Reservations by User ID
	list, err := database.ListSubdomainReservationsByUserID(userID)
	if err != nil {
		t.Fatalf("ListSubdomainReservationsByUserID failed: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 reservations, got %d", len(list))
	}

	// 6. Delete Expired Reservations after cutoff
	// Let's create another reservation that expired 4 days ago
	pastExpiry := time.Now().UTC().AddDate(0, 0, -4)
	resExpired := &SubdomainReservation{
		UserID:    userID,
		Subdomain: "expired-old",
		Domain:    "lfr-demo.se",
		ExpiresAt: &pastExpiry,
	}
	if err := database.CreateSubdomainReservation(resExpired); err != nil {
		t.Fatalf("CreateSubdomainReservation failed: %v", err)
	}

	// Verify it is in list
	allList, err := database.ListAllSubdomainReservations()
	if err != nil {
		t.Fatalf("ListAllSubdomainReservations failed: %v", err)
	}
	if len(allList) != 3 {
		t.Errorf("expected 3 total reservations, got %d", len(allList))
	}

	// Run cleanup with cutoff: 3 days ago
	cutoff := time.Now().UTC().AddDate(0, 0, -3)
	if err := database.DeleteExpiredSubdomainReservations(cutoff); err != nil {
		t.Fatalf("DeleteExpiredSubdomainReservations failed: %v", err)
	}

	// Verify only the 4-day-old expired reservation was deleted (2 remain)
	allListAfter, err := database.ListAllSubdomainReservations()
	if err != nil {
		t.Fatalf("ListAllSubdomainReservations failed: %v", err)
	}
	if len(allListAfter) != 2 {
		t.Errorf("expected 2 reservations after cleanup, got %d", len(allListAfter))
	}

	// 7. Delete specific reservation
	if err := database.DeleteSubdomainReservation(res1.ID); err != nil {
		t.Fatalf("DeleteSubdomainReservation failed: %v", err)
	}

	_, err = database.GetSubdomainReservation(res1.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after deletion, got %v", err)
	}
}

func TestSubdomainACL(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "tunnel-db-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir) //nolint:errcheck

	dbPath := filepath.Join(tempDir, "tunnel.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer database.Close() //nolint:errcheck

	// 1. Create ACL permissions
	acl1 := &SubdomainACL{
		Subdomain: "client-poc",
		Domain:    "lfr-demo.se",
		Identity:  "user:colleague-123",
		Name:      "John Doe",
		Email:     "john.doe@liferay.com",
	}

	if err := database.CreateSubdomainACL(acl1); err != nil {
		t.Fatalf("CreateSubdomainACL failed: %v", err)
	}
	if acl1.ID == 0 {
		t.Error("expected ACL entry ID to be populated")
	}

	// Test unique constraints (duplicate subdomain+domain+identity should fail)
	aclDup := &SubdomainACL{
		Subdomain: "client-poc",
		Domain:    "lfr-demo.se",
		Identity:  "user:colleague-123",
	}
	if err := database.CreateSubdomainACL(aclDup); err == nil {
		t.Error("expected duplicate ACL to fail, but it succeeded")
	}

	// 2. Fetch ACL by ID
	fetched, err := database.GetSubdomainACL(acl1.ID)
	if err != nil {
		t.Fatalf("GetSubdomainACL failed: %v", err)
	}
	if fetched.Identity != "user:colleague-123" {
		t.Errorf("expected Identity to be user:colleague-123, got %s", fetched.Identity)
	}

	// 3. Create Expiring ACL (guest invitation)
	expiry := time.Now().UTC().Add(1 * time.Hour)
	aclGuest := &SubdomainACL{
		Subdomain: "client-poc",
		Domain:    "lfr-demo.se",
		Identity:  "guest:token-456",
		ExpiresAt: &expiry,
	}
	if err := database.CreateSubdomainACL(aclGuest); err != nil {
		t.Fatalf("CreateSubdomainACL failed: %v", err)
	}

	// 4. List ACL entries for Subdomain
	list, err := database.ListSubdomainACL("client-poc", "lfr-demo.se")
	if err != nil {
		t.Fatalf("ListSubdomainACL failed: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 ACL entries, got %d", len(list))
	}

	// 5. Clean up expired ACLs
	pastExpiry := time.Now().UTC().AddDate(0, 0, -4)
	aclExpired := &SubdomainACL{
		Subdomain: "client-poc",
		Domain:    "lfr-demo.se",
		Identity:  "guest:expired-789",
		ExpiresAt: &pastExpiry,
	}
	if err := database.CreateSubdomainACL(aclExpired); err != nil {
		t.Fatalf("CreateSubdomainACL failed: %v", err)
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -3)
	if err := database.DeleteExpiredSubdomainACLs(cutoff); err != nil {
		t.Fatalf("DeleteExpiredSubdomainACLs failed: %v", err)
	}

	// Verify only the 4-day-old expired ACL was deleted (2 remain)
	listAfter, err := database.ListSubdomainACL("client-poc", "lfr-demo.se")
	if err != nil {
		t.Fatalf("ListSubdomainACL failed: %v", err)
	}
	if len(listAfter) != 2 {
		t.Errorf("expected 2 ACL entries after cleanup, got %d", len(listAfter))
	}

	// 6. Delete specific ACL
	if err := database.DeleteSubdomainACL(acl1.ID); err != nil {
		t.Fatalf("DeleteSubdomainACL failed: %v", err)
	}

	_, err = database.GetSubdomainACL(acl1.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after deletion, got %v", err)
	}
}

func TestGatewayRuns(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "tunnel-db-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir) //nolint:errcheck

	dbPath := filepath.Join(tempDir, "tunnel.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close() //nolint:errcheck

	// 1. Record first gateway start
	t1 := time.Now().UTC().Add(-10 * time.Minute)
	if err := database.RecordGatewayStart(t1); err != nil {
		t.Fatalf("RecordGatewayStart failed: %v", err)
	}

	// Verify first run
	runs, err := database.GetGatewayRuns(10)
	if err != nil {
		t.Fatalf("GetGatewayRuns failed: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].EndTime != nil {
		t.Errorf("expected EndTime to be nil for active run, got %v", runs[0].EndTime)
	}

	// 2. Record clean shutdown
	if err := database.RecordGatewayCleanShutdown(); err != nil {
		t.Fatalf("RecordGatewayCleanShutdown failed: %v", err)
	}

	// Verify run is closed
	runs, _ = database.GetGatewayRuns(10)
	if runs[0].EndTime == nil {
		t.Error("expected EndTime to be populated after clean shutdown")
	}

	// 3. Record second start (simulating crash recovery)
	// We start it without clean shutdown to test if RecordGatewayStart closes the previous open run
	t2 := time.Now().UTC().Add(-5 * time.Minute)
	// Make previous run open again by updating its end_time to NULL
	_, _ = database.conn.Exec("UPDATE gateway_runs SET end_time = NULL")

	if err := database.RecordGatewayStart(t2); err != nil {
		t.Fatalf("RecordGatewayStart failed: %v", err)
	}

	// Verify
	runs, _ = database.GetGatewayRuns(10)
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}
	// The oldest run (runs[1]) should have its EndTime set to the start time of the new run (t2)
	if runs[1].EndTime == nil {
		t.Error("expected previous run to be closed by new run start")
	} else if !runs[1].EndTime.Equal(t2) {
		t.Errorf("expected previous run EndTime to be %v, got %v", t2, runs[1].EndTime)
	}
	// The new run (runs[0]) should have nil EndTime
	if runs[0].EndTime != nil {
		t.Errorf("expected current run EndTime to be nil, got %v", runs[0].EndTime)
	}
}

func TestAnalyticsWithNullDates(t *testing.T) {
	database, tmpDir := setupTestDB(t)
	defer cleanupTestDB(database, tmpDir)

	// Create user
	user := &User{
		ID:     "analyt-user",
		Email:  "analytics-test@liferay.com",
		Role:   "user",
		Status: "approved",
	}
	if err := database.CreateUser(user); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Insert metric row with invalid recorded_at timestamp string (which strftime cannot parse, yielding NULL)
	_, err := database.conn.Exec(`
		INSERT INTO tunnel_metrics (user_id, subdomain_prefix, full_host, bytes_in, bytes_out, connected_at, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, user.ID, "pjrtest", "pjrtest.example.com", 100, 200, time.Now().UTC(), "invalid-timestamp-string-value")
	if err != nil {
		t.Fatalf("failed to insert metric with invalid date: %v", err)
	}

	// Verify GetUserAnalytics runs successfully and parses invalid date as "Unknown"
	userStats, err := database.GetUserAnalytics(user.ID, 30)
	if err != nil {
		t.Fatalf("GetUserAnalytics failed on invalid date: %v", err)
	}
	if len(userStats.Daily) != 1 {
		t.Fatalf("expected 1 daily stats row, got %d", len(userStats.Daily))
	}
	if userStats.Daily[0].Date != "Unknown" {
		t.Errorf("expected daily date to fallback to 'Unknown', got %q", userStats.Daily[0].Date)
	}

	// Verify GetGlobalAnalytics runs successfully and parses invalid date as "Unknown"
	globalStats, err := database.GetGlobalAnalytics(30)
	if err != nil {
		t.Fatalf("GetGlobalAnalytics failed on invalid date: %v", err)
	}
	if len(globalStats.Daily) != 1 {
		t.Fatalf("expected 1 global daily stats row, got %d", len(globalStats.Daily))
	}
	if globalStats.Daily[0].Date != "Unknown" {
		t.Errorf("expected global daily date to fallback to 'Unknown', got %q", globalStats.Daily[0].Date)
	}
}
