import re

with open("pkg/server/server.go", "r") as f:
    content = f.read()

# 1. line 856: if err := s.mailSender.Send(user.Email, subject, body); err != nil {
content = content.replace(
    `if err := s.mailSender.Send(user.Email, subject, body); err != nil {`,
    `plainBody := fmt.Sprintf("Hi %s,\n\nPlease complete your registration by visiting: %s\n\nIf you did not request this, you can report it at: %s", greetingName, verifyURL, reportLink)\n\t\tif err := s.mailSender.Send(user.Email, subject, body, plainBody); err != nil {`
)

# 2. line 935: go s.mailSender.Send(s.cfg.AdminNotificationEmail, subject, body)
content = content.replace(
    `go s.mailSender.Send(s.cfg.AdminNotificationEmail, subject, body)`,
    `go s.mailSender.Send(s.cfg.AdminNotificationEmail, subject, body, fmt.Sprintf("New user registered: %s", u.Email))`
)

# 3. line 984: if err := s.mailSender.Send(s.cfg.AdminNotificationEmail, subject, body); err != nil {
content = content.replace(
    `if err := s.mailSender.Send(s.cfg.AdminNotificationEmail, subject, body); err != nil {`,
    `plainBody := fmt.Sprintf("A new user (%s) requires approval.", req.Email)\n\t\t\tif err := s.mailSender.Send(s.cfg.AdminNotificationEmail, subject, body, plainBody); err != nil {`
)

# 4. line 1069: if err := s.mailSender.Send(user.Email, subject, body); err != nil {
content = content.replace(
    `if err := s.mailSender.Send(user.Email, subject, body); err != nil {`,
    `plainBody := fmt.Sprintf("Your registration has been approved. Claim your token here: %s", claimURL)\n\t\tif err := s.mailSender.Send(user.Email, subject, body, plainBody); err != nil {`
)

# 5. line 1493: go s.mailSender.Send(req.Email, "Your magic login link", body) //nolint:errcheck
content = content.replace(
    `go s.mailSender.Send(req.Email, "Your magic login link", body) //nolint:errcheck`,
    `plainBody := fmt.Sprintf("Hi %s,\n\nUse this link to log in (expires in 15 minutes):\n%s\n\nReport abuse here:\n%s", greetingName, link, reportLink)\n\t\tgo s.mailSender.Send(req.Email, "Your magic login link", body, plainBody) //nolint:errcheck`
)

# 6. line 1764: go s.mailSender.Send(req.Email, subject, body)
content = content.replace(
    `go s.mailSender.Send(req.Email, subject, body)`,
    `plainBody := fmt.Sprintf("Hi there,\n\nYou have been invited by an administrator to use the Liferay Tunnel portal.\n\nLog in here: %s\n\nDecline here: %s", magicLink, declineLink)\n\t\tgo s.mailSender.Send(req.Email, subject, body, plainBody)`
)

# 7. line 2147: if err := s.mailSender.Send(s.cfg.AdminNotificationEmail, subject, htmlBody); err != nil {
content = content.replace(
    `if err := s.mailSender.Send(s.cfg.AdminNotificationEmail, subject, htmlBody); err != nil {`,
    `if err := s.mailSender.Send(s.cfg.AdminNotificationEmail, subject, htmlBody, "An IP address has been blacklisted."); err != nil {`
)

with open("pkg/server/server.go", "w") as f:
    f.write(content)
