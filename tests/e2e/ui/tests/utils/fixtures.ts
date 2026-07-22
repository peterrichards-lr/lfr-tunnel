import { test as base, expect } from '@playwright/test';

export const test = base.extend({
  page: async ({ page, context }, use) => {
    try {
      await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    } catch (e) {
      console.warn("Failed to grant clipboard permissions in this context:", e);
    }

    const errors: Error[] = [];
    page.on('pageerror', (err) => {
      errors.push(err);
    });

    await use(page);

    if (errors.length > 0) {
      throw new Error(`Uncaught exception detected in page: ${errors[0].message}\nStack:\n${errors[0].stack}`);
    }
  },
});

export { expect };
