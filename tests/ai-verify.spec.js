const { test, expect } = require('@playwright/test');
const fs = require('fs');

const BASE = 'http://localhost:9080';

async function login(page) {
  await page.goto(`${BASE}/admin/login`);
  await page.fill('#username', 'admin');
  await page.fill('#password', 'admin123');
  await page.click('button[type="submit"]');
  await page.waitForURL('**/admin/dashboard**');
}

async function aiEditAndApprove(page, file, instruction) {
  await page.goto(`${BASE}/admin/ai?file=${file}`);
  const original = await page.locator('#originalContent').inputValue();

  await page.fill('#instruction', instruction);

  // Capture network response
  const responsePromise = page.waitForResponse(resp =>
    resp.url().includes('/admin/ai') && resp.request().method() === 'POST'
  );
  await page.click('button:has-text("Generate with AI")');
  const resp = await responsePromise;
  const data = await resp.json();

  if (data.Error) {
    return { success: false, error: data.Error, original };
  }

  // Verify result panel is visible
  await expect(page.locator('#resultPanel')).toBeVisible({ timeout: 3000 });

  // Get the AI-generated content from textarea
  const aiContent = await page.locator('#aiContent').inputValue();

  // Click Approve & Save
  const approvePromise = page.waitForResponse(r => r.url().includes('/admin/ai/approve'));
  await page.click('button:has-text("Approve")');
  const approveResp = await approvePromise;

  // Wait for redirect to editor
  await page.waitForURL(/\/admin\/edit/, { timeout: 5000 });

  // Read back from editor to confirm save
  const savedContent = await page.locator('#editor').inputValue();

  return { success: true, original, aiContent, savedContent };
}

async function restore(page, file, content) {
  await page.goto(`${BASE}/admin/edit?file=${file}`);
  await page.locator('#editor').fill(content);
  await page.click('button:has-text("Save")');
  await page.waitForURL(/saved=1/);
}

// ─── Test 1: clients.html border change ─────────────────────────────

test('AI Edit 1: Change gallery-card border in clients.html', async ({ page }) => {
  test.setTimeout(120000);
  await login(page);

  const result = await aiEditAndApprove(
    page, '/clients.html',
    'Change the gallery-card CSS class border to 1px solid black'
  );

  expect(result.success).toBe(true);
  console.log('AI content has black border:', result.aiContent.includes('1px solid black'));
  console.log('Saved content has black border:', result.savedContent.includes('1px solid black'));
  expect(result.savedContent).toContain('1px solid black');

  // Verify on disk
  const diskContent = fs.readFileSync('/home/production/DEPLOYED/pcsvoip.datalytique.com/clients.html', 'utf-8');
  console.log('DISK file has black border:', diskContent.includes('1px solid black'));
  expect(diskContent).toContain('1px solid black');

  // Restore
  await restore(page, '/clients.html', result.original);
  const restored = fs.readFileSync('/home/production/DEPLOYED/pcsvoip.datalytique.com/clients.html', 'utf-8');
  console.log('Restored successfully:', !restored.includes('1px solid black'));
});

// ─── Test 2: test.html title change ─────────────────────────────────

test('AI Edit 2: Change title in test.html', async ({ page }) => {
  test.setTimeout(120000);
  await login(page);

  const result = await aiEditAndApprove(
    page, '/test.html',
    'Change the page title to "PCS VoIP Map Demo"'
  );

  expect(result.success).toBe(true);
  console.log('Saved has new title:', result.savedContent.includes('PCS VoIP Map Demo'));
  expect(result.savedContent).toContain('PCS VoIP Map Demo');

  // Verify on disk
  const disk = fs.readFileSync('/home/production/DEPLOYED/pcsvoip.datalytique.com/test.html', 'utf-8');
  console.log('DISK has new title:', disk.includes('PCS VoIP Map Demo'));
  expect(disk).toContain('PCS VoIP Map Demo');

  await restore(page, '/test.html', result.original);
});

// ─── Test 3: about.html title change ────────────────────────────────

test('AI Edit 3: Change title in about.html', async ({ page }) => {
  test.setTimeout(120000);
  await login(page);

  const result = await aiEditAndApprove(
    page, '/about.html',
    'Change the page title tag content to "About PCS VoIP - Enterprise Communications"'
  );

  expect(result.success).toBe(true);
  console.log('Saved has new title:', result.savedContent.includes('Enterprise Communications'));
  expect(result.savedContent).toContain('Enterprise Communications');

  const disk = fs.readFileSync('/home/production/DEPLOYED/pcsvoip.datalytique.com/about.html', 'utf-8');
  console.log('DISK has new title:', disk.includes('Enterprise Communications'));
  expect(disk).toContain('Enterprise Communications');

  await restore(page, '/about.html', result.original);
});

// ─── Test 4: CSS file edit ──────────────────────────────────────────

test('AI Edit 4: Modify a CSS property in a JSON file', async ({ page }) => {
  test.setTimeout(120000);
  await login(page);

  // Edit faq.html - change something simple
  const result = await aiEditAndApprove(
    page, '/faq.html',
    'Add a meta tag for author with content "PCS VoIP Team" right after the existing meta charset tag'
  );

  expect(result.success).toBe(true);
  console.log('Saved has author meta:', result.savedContent.includes('PCS VoIP Team'));
  expect(result.savedContent).toContain('PCS VoIP Team');

  const disk = fs.readFileSync('/home/production/DEPLOYED/pcsvoip.datalytique.com/faq.html', 'utf-8');
  console.log('DISK has author meta:', disk.includes('PCS VoIP Team'));
  expect(disk).toContain('PCS VoIP Team');

  await restore(page, '/faq.html', result.original);
});

// ─── Test 5: Verify file content via public URL after AI edit ───────

test('AI Edit 5: Verify changes are publicly accessible after approve', async ({ page }) => {
  test.setTimeout(120000);
  await login(page);

  const result = await aiEditAndApprove(
    page, '/test.html',
    'Add an HTML comment at the very top of the file: <!-- AI-VERIFIED-EDIT -->'
  );

  expect(result.success).toBe(true);
  expect(result.savedContent).toContain('AI-VERIFIED-EDIT');

  // Now fetch the file as a public user (no auth needed for static files)
  const publicResp = await page.request.get(`${BASE}/test.html`);
  const publicContent = await publicResp.text();
  console.log('Public page has AI edit:', publicContent.includes('AI-VERIFIED-EDIT'));
  expect(publicContent).toContain('AI-VERIFIED-EDIT');

  await restore(page, '/test.html', result.original);
  console.log('All verified - AI edits persist to disk and are publicly served');
});
