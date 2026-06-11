import re

filepath = 'pkg/server/server_test.go'
with open(filepath, 'r') as f:
    data = f.read()

# Replace all "mysecret" with "lfr_pat_mysecret"
data = data.replace('"mysecret"', '"lfr_pat_mysecret"')

# We also need to make sure every NewServer call that used AuthToken now has a DB
db_setup = """	dbPath := filepath.Join(t.TempDir(), "test.db")
	cfg.DBPath = dbPath
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()
	srv.db.CreateUser(&db.User{Email: "test@example.com", Role: "admin", Status: "approved"})
	patHashBytes := sha256.Sum256([]byte("lfr_pat_mysecret"))
	srv.db.CreatePAT(&db.PersonalAccessToken{UserID: "test@example.com", TokenHash: hex.EncodeToString(patHashBytes[:]), TokenPrefix: "lfr_pat_myse"})
"""

data = re.sub(r'\tsrv, err := NewServer\(cfg\)\n\tif err != nil \{\n\t\tt\.Fatalf\("failed to create server: %v", err\)\n\t\}\n\tdefer srv\.Stop\(\)\n', db_setup, data)

with open(filepath, 'w') as f:
    f.write(data)
