# Custom Maintenance Page Feature

## Goal
Update the embedded default maintenance page to resemble the layout and style of the 502 gateway offline page, and introduce a new admin system setting allowing administrators to point to a custom maintenance page file (HTML, JSON, or TXT) to serve instead of the embedded default. Additionally, redesign the System Settings UI to use a responsive two-column layout.

## Proposed Changes

### Database & API layer
#### [MODIFY] pkg/server/api.go
- Update `handleAdminGetSystemSettings` to read the `maintenance_page_path` setting from the database and return it in the JSON response.
- Update `handleAdminUpdateSystemSettings` to parse and save `maintenance_page_path` to the database.

### Application Logic layer
#### [MODIFY] pkg/server/server.go
- Update `handleVisitorMaintenancePage` to check if `maintenance_page_path` is configured and points to a readable file. If it does, serve the contents of that file. Otherwise, fallback to the embedded `static/maintenance.html` file. 
- Update `handleAdminMaintenance` to similarly load the custom file content (if configured and valid) when triggering the `nginxManager.Enable` (Iron Curtain Mode), allowing Nginx to serve the custom file instead of the embedded template.

### UI & Aesthetics layer
#### [MODIFY] pkg/server/static/maintenance.html
- Refactor the markup and embedded styles to closely match the aesthetic and structure of the 502 Gateway Error page (`offline.html`), while preserving the Amber warning colors and dynamic translation/countdown logic.

#### [MODIFY] pkg/server/dashboard.html
- Refactor the `#settings` tab layout to use a responsive grid or flexbox layout with two columns.
- Ensure the columns wrap to a single column on tablet/mobile screens.
- Add a new section/card in the settings page for "Maintenance Page Settings".
- Include a dropdown or text input to specify the custom file path, leaving it empty or set to "Embedded Default" for the original behavior.

#### [MODIFY] pkg/server/static/dashboard.js
- Update the system settings loading and saving logic to populate and persist the new `maintenance_page_path` property.

## Verification Plan
1. Start the server and verify the new setting appears in the reorganized System Settings tab.
2. Resize the browser window to verify the two-column layout responsively wraps.
3. Enable Soft Maintenance Mode and verify the new default design looks like the 502 page.
4. Configure a custom JSON and HTML file path in the settings, enable maintenance mode, and verify the custom file is successfully served instead of the default.
5. Verify Iron Curtain Mode also correctly picks up and writes the custom file to the Nginx staging path.
