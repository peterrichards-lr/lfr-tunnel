import os
files = ["tests/e2e/ui/tests/dashboard.spec.ts", "tests/e2e/ui/tests/portal_v2_login.spec.ts"]
for f in files:
    with open(f, "r") as file:
        content = file.read()
    content = content.replace('h2:has-text("Dashboard Overview")', 'h1:has-text("Dashboard Overview")')
    content = content.replace("locator('h2', { hasText: 'Dashboard' })", "locator('h1', { hasText: 'Dashboard Overview' })")
    with open(f, "w") as file:
        file.write(content)
