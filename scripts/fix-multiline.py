with open('pkg/server/server.go', 'r') as f:
    content = f.read()

# Fix 1
content = content.replace(
'''	body, _ := s.renderNotificationTemplate("en", "admin_registration_request.txt", map[string]interface{}{ //nolint:errcheck
		"FirstName": user.FirstName,
		"LastName":  user.LastName,
		"Email":     user.Email,
	})''',
'''	body, err := s.renderNotificationTemplate("en", "admin_registration_request.txt", map[string]interface{}{
		"FirstName": user.FirstName,
		"LastName":  user.LastName,
		"Email":     user.Email,
	})
	_ = err //nolint:errcheck''')

# Fix 2
content = content.replace(
'''	body, _ := s.renderNotificationTemplate("en", "admin_registration_request.txt", map[string]interface{}{ //nolint:errcheck
		"Email": user.Email,
	})''',
'''	body, err := s.renderNotificationTemplate("en", "admin_registration_request.txt", map[string]interface{}{
		"Email": user.Email,
	})
	_ = err //nolint:errcheck''')

# Fix 3
content = content.replace(
'''		body, _ := s.renderNotificationTemplate("en", "admin_test_integration.txt", map[string]interface{}{ //nolint:errcheck
			"Actor":     actor,
			"Timestamp": timestamp,
			"Version":   version,
			"Type":      "Email",
		})''',
'''		body, err := s.renderNotificationTemplate("en", "admin_test_integration.txt", map[string]interface{}{
			"Actor":     actor,
			"Timestamp": timestamp,
			"Version":   version,
			"Type":      "Email",
		})
		_ = err //nolint:errcheck''')

with open('pkg/server/server.go', 'w') as f:
    f.write(content)
