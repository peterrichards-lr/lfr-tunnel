const { chromium } = require('playwright');
(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage();
  await page.goto('http://127.0.0.1:4041');
  await page.waitForTimeout(1000);
  await page.screenshot({ path: '/Users/peterrichards/.gemini/antigravity/brain/0928c092-124c-4270-8439-7e6842a2d09d/.tempmediaStorage/inspector_ui.png' });
  await browser.close();
})();
