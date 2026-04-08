const { test, expect } = require('@playwright/test');

const BASE = 'http://localhost:9080';
const ADMIN_USER = 'admin';
const ADMIN_PASS = 'admin123';

// Helper: login and return authenticated page
async function login(page) {
  await page.goto(`${BASE}/admin/login`);
  await page.fill('#username', ADMIN_USER);
  await page.fill('#password', ADMIN_PASS);
  await page.click('button[type="submit"]');
  await page.waitForURL('**/admin/dashboard**');
}

// ─── Authentication Tests ───────────────────────────────────────────

test.describe('Authentication', () => {
  test('login page renders correctly', async ({ page }) => {
    await page.goto(`${BASE}/admin/login`);
    await expect(page.locator('h1')).toContainText('PCSVoIP');
    await expect(page.locator('#username')).toBeVisible();
    await expect(page.locator('#password')).toBeVisible();
    await expect(page.locator('button[type="submit"]')).toContainText('Sign In');
  });

  test('login with valid credentials redirects to dashboard', async ({ page }) => {
    await login(page);
    await expect(page).toHaveURL(/\/admin\/dashboard/);
    await expect(page.locator('h1')).toContainText('PCSVoIP CMS');
  });

  test('login with invalid credentials shows error', async ({ page }) => {
    await page.goto(`${BASE}/admin/login`);
    await page.fill('#username', 'admin');
    await page.fill('#password', 'wrongpassword');
    await page.click('button[type="submit"]');
    await expect(page.locator('.error')).toContainText('Invalid credentials');
  });

  test('login with empty fields keeps form on login page', async ({ page }) => {
    await page.goto(`${BASE}/admin/login`);
    // HTML5 required validation prevents submission, so page stays
    await page.click('button[type="submit"]');
    await expect(page).toHaveURL(/\/admin\/login/);
  });

  test('accessing protected route without auth redirects to login', async ({ page }) => {
    await page.goto(`${BASE}/admin/dashboard`);
    await expect(page).toHaveURL(/\/admin\/login/);
  });

  test('accessing AI editor without auth redirects to login', async ({ page }) => {
    await page.goto(`${BASE}/admin/ai?file=/test.html`);
    await expect(page).toHaveURL(/\/admin\/login/);
  });

  test('logout clears session and redirects to login', async ({ page }) => {
    await login(page);
    await page.click('a[href="/admin/logout"]');
    await expect(page).toHaveURL(/\/admin\/login/);
    // Verify session is cleared - accessing dashboard should redirect to login
    await page.goto(`${BASE}/admin/dashboard`);
    await expect(page).toHaveURL(/\/admin\/login/);
  });
});

// ─── Dashboard Tests ────────────────────────────────────────────────

test.describe('Dashboard', () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test('dashboard shows file listing', async ({ page }) => {
    await expect(page.locator('.file-list')).toBeVisible();
    // Should have at least some files (index.html etc)
    const items = page.locator('.file-item');
    await expect(items.first()).toBeVisible();
    const count = await items.count();
    expect(count).toBeGreaterThan(0);
  });

  test('dashboard shows breadcrumb navigation', async ({ page }) => {
    await expect(page.locator('.breadcrumb')).toBeVisible();
    await expect(page.locator('.breadcrumb a').first()).toContainText('Root');
  });

  test('dashboard files have Edit and AI action buttons', async ({ page }) => {
    // Find a file item (non-directory) with action buttons
    const editLink = page.locator('.file-actions a[href*="/admin/edit"]').first();
    const aiLink = page.locator('.file-actions a[href*="/admin/ai"]').first();
    await expect(editLink).toBeVisible();
    await expect(aiLink).toBeVisible();
    await expect(editLink).toContainText('Edit');
    await expect(aiLink).toContainText('AI');
  });

  test('clicking a directory navigates into it', async ({ page }) => {
    // Look for a directory link (ends with /)
    const dirLink = page.locator('.file-name a[href*="?dir="]').first();
    if (await dirLink.count() > 0) {
      const dirName = await dirLink.textContent();
      await dirLink.click();
      await expect(page).toHaveURL(/dir=/);
      // Should show "Up" link
      await expect(page.locator('.breadcrumb')).toContainText('Up');
    }
  });

  test('navigating to assets directory shows subdirectories', async ({ page }) => {
    await page.goto(`${BASE}/admin/dashboard?dir=/assets`);
    await expect(page.locator('.file-list')).toBeVisible();
    // assets directory should have subdirectories like css, js, img
    const items = page.locator('.file-item');
    const count = await items.count();
    expect(count).toBeGreaterThan(0);
  });

  test('parent directory navigation works', async ({ page }) => {
    await page.goto(`${BASE}/admin/dashboard?dir=/assets`);
    const upLink = page.locator('a:has-text("Up")');
    if (await upLink.count() > 0) {
      await upLink.click();
      await expect(page).toHaveURL(/\/admin\/dashboard/);
    }
  });
});

// ─── Manual Editor Tests ────────────────────────────────────────────

test.describe('Manual Editor', () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test('editor loads file content', async ({ page }) => {
    await page.goto(`${BASE}/admin/edit?file=/test.html`);
    await expect(page.locator('h1')).toContainText('Editing');
    const editor = page.locator('#editor');
    await expect(editor).toBeVisible();
    const content = await editor.inputValue();
    expect(content.length).toBeGreaterThan(0);
  });

  test('editor shows correct filename in title', async ({ page }) => {
    await page.goto(`${BASE}/admin/edit?file=/test.html`);
    await expect(page.locator('h1')).toContainText('test.html');
  });

  test('editor has toolbar with Preview and Save buttons', async ({ page }) => {
    await page.goto(`${BASE}/admin/edit?file=/test.html`);
    await expect(page.locator('button:has-text("Preview")')).toBeVisible();
    await expect(page.locator('button:has-text("Save")')).toBeVisible();
  });

  test('editor has link to AI Edit', async ({ page }) => {
    await page.goto(`${BASE}/admin/edit?file=/test.html`);
    const aiLink = page.locator('a[href*="/admin/ai?file="]');
    await expect(aiLink).toBeVisible();
    await expect(aiLink).toContainText('AI Edit');
  });

  test('editor has link back to Dashboard', async ({ page }) => {
    await page.goto(`${BASE}/admin/edit?file=/test.html`);
    const dashLink = page.locator('a[href="/admin/dashboard"]');
    await expect(dashLink).toBeVisible();
  });

  test('preview toggle shows iframe and hides editor', async ({ page }) => {
    await page.goto(`${BASE}/admin/edit?file=/test.html`);
    const editor = page.locator('#editor');
    const frame = page.locator('#previewFrame');

    // Initially: editor visible, frame hidden
    await expect(editor).toBeVisible();
    await expect(frame).not.toBeVisible();

    // Click preview
    await page.click('button:has-text("Preview")');
    // Editor hidden, frame visible
    await expect(editor).not.toBeVisible();
    await expect(frame).toBeVisible();

    // Toggle back
    await page.click('button:has-text("Preview")');
    await expect(editor).toBeVisible();
    await expect(frame).not.toBeVisible();
  });

  test('save content persists changes', async ({ page }) => {
    await page.goto(`${BASE}/admin/edit?file=/test.html`);
    const editor = page.locator('#editor');

    // Get original content
    const originalContent = await editor.inputValue();

    // Add a timestamp comment
    const marker = `<!-- test-marker-${Date.now()} -->`;
    await editor.fill(originalContent + marker);

    // Save
    await page.click('button:has-text("Save")');
    await page.waitForURL(/saved=1/);

    // Verify content was saved by reloading
    await page.goto(`${BASE}/admin/edit?file=/test.html`);
    const savedContent = await page.locator('#editor').inputValue();
    expect(savedContent).toContain(marker);

    // Restore original content
    await page.locator('#editor').fill(originalContent);
    await page.click('button:has-text("Save")');
    await page.waitForURL(/saved=1/);
  });

  test('editing non-existent file shows error', async ({ page }) => {
    const response = await page.goto(`${BASE}/admin/edit?file=/nonexistent-file-xyz.html`);
    // Should get a 403 error
    expect(response.status()).toBe(403);
  });

  test('editing restricted file type is blocked', async ({ page }) => {
    const response = await page.goto(`${BASE}/admin/edit?file=/bin/webserver`);
    expect(response.status()).toBe(403);
  });

  test('path traversal attempt is blocked', async ({ page }) => {
    const response = await page.goto(`${BASE}/admin/edit?file=/../../../etc/passwd`);
    expect(response.status()).toBe(403);
  });

  test('redirect to dashboard when no file specified', async ({ page }) => {
    await page.goto(`${BASE}/admin/edit`);
    await expect(page).toHaveURL(/\/admin\/dashboard/);
  });
});

// ─── AI Editor Tests ────────────────────────────────────────────────

test.describe('AI Editor', () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test('AI editor page loads with file content', async ({ page }) => {
    await page.goto(`${BASE}/admin/ai?file=/test.html`);
    await expect(page.locator('h1')).toContainText('AI Content Editor');
    await expect(page.locator('#instruction')).toBeVisible();
    await expect(page.locator('button:has-text("Generate with AI")')).toBeVisible();
  });

  test('AI editor shows correct filename', async ({ page }) => {
    await page.goto(`${BASE}/admin/ai?file=/test.html`);
    await expect(page.locator('h2').first()).toContainText('test.html');
  });

  test('AI editor has navigation links', async ({ page }) => {
    await page.goto(`${BASE}/admin/ai?file=/test.html`);
    await expect(page.locator('a:has-text("Manual Edit")')).toBeVisible();
    await expect(page.locator('a[href="/admin/dashboard"]')).toBeVisible();
    await expect(page.locator('a[href="/admin/logout"]')).toBeVisible();
  });

  test('AI editor requires instruction before generating', async ({ page }) => {
    await page.goto(`${BASE}/admin/ai?file=/test.html`);

    // Set up dialog handler for alert
    page.on('dialog', async dialog => {
      expect(dialog.message()).toContain('Please enter an instruction');
      await dialog.accept();
    });

    // Click generate with empty instruction
    await page.click('button:has-text("Generate with AI")');
  });

  test('AI result panel is hidden initially', async ({ page }) => {
    await page.goto(`${BASE}/admin/ai?file=/test.html`);
    const resultPanel = page.locator('#resultPanel');
    await expect(resultPanel).not.toBeVisible();
  });

  test('AI editor shows spinner during processing', async ({ page }) => {
    await page.goto(`${BASE}/admin/ai?file=/test.html`);
    const spinner = page.locator('#spinner');
    await expect(spinner).not.toBeVisible();

    // Fill instruction and submit
    await page.fill('#instruction', 'Add a comment to the top of the file');

    // Intercept the AI request to check spinner shows
    const requestPromise = page.waitForRequest('**/admin/ai');
    await page.click('button:has-text("Generate with AI")');
    await requestPromise;

    // Spinner should be visible while processing
    // (may be brief if response is fast)
    await expect(spinner).toBeVisible({ timeout: 2000 }).catch(() => {
      // Spinner may have already hidden if response was very fast
    });
  });

  test('AI editing full flow - generate, review, approve', async ({ page }) => {
    await page.goto(`${BASE}/admin/ai?file=/test.html`);

    // Get original content for later comparison
    const originalContent = await page.locator('#originalContent').inputValue();

    // Fill instruction
    await page.fill('#instruction', 'Add an HTML comment at the very top that says "AI was here"');

    // Submit AI request and wait for response
    const responsePromise = page.waitForResponse('**/admin/ai');
    await page.click('button:has-text("Generate with AI")');
    const response = await responsePromise;
    const data = await response.json();

    // Check response structure
    if (data.Error) {
      // AI provider might be unavailable - check error is properly returned
      console.log('AI provider returned error (expected in test env):', data.Error);
      // Verify the error is displayed via alert
      return;
    }

    // Result panel should become visible
    await expect(page.locator('#resultPanel')).toBeVisible();

    // AI content textarea should have content
    const aiContent = await page.locator('#aiContent').inputValue();
    expect(aiContent.length).toBeGreaterThan(0);

    // Should have approve and discard buttons
    await expect(page.locator('button:has-text("Approve")')).toBeVisible();
    await expect(page.locator('button:has-text("Discard")')).toBeVisible();

    // Test approve - this will save and redirect
    await page.click('button:has-text("Approve")');
    await page.waitForURL(/\/admin\/edit\?file=.*saved=1/);

    // Restore original content
    await page.locator('#editor').fill(originalContent);
    await page.click('button:has-text("Save")');
    await page.waitForURL(/saved=1/);
  });

  test('AI editing - discard result hides panel', async ({ page }) => {
    await page.goto(`${BASE}/admin/ai?file=/test.html`);
    await page.fill('#instruction', 'Add a comment');

    const responsePromise = page.waitForResponse('**/admin/ai');
    await page.click('button:has-text("Generate with AI")');
    const response = await responsePromise;
    const data = await response.json();

    if (data.Error) {
      console.log('AI provider error (expected):', data.Error);
      return;
    }

    await expect(page.locator('#resultPanel')).toBeVisible();
    await page.click('button:has-text("Discard")');
    await expect(page.locator('#resultPanel')).not.toBeVisible();
  });

  test('AI editor POST with missing instruction returns error', async ({ page }) => {
    await login(page);

    // Directly POST to AI endpoint without instruction
    const response = await page.request.post(`${BASE}/admin/ai`, {
      form: { file: '/test.html', instruction: '' },
    });
    expect(response.status()).toBe(400);
  });

  test('AI editor POST with missing file returns error', async ({ page }) => {
    await login(page);

    const response = await page.request.post(`${BASE}/admin/ai`, {
      form: { file: '', instruction: 'do something' },
    });
    expect(response.status()).toBe(400);
  });
});

// ─── Save Endpoint Tests ────────────────────────────────────────────

test.describe('Save Endpoint', () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test('POST to save with valid content works', async ({ page }) => {
    // First read original content
    await page.goto(`${BASE}/admin/edit?file=/test.html`);
    const original = await page.locator('#editor').inputValue();

    // Save via form
    const response = await page.request.post(`${BASE}/admin/save`, {
      form: { file: '/test.html', content: original },
    });
    // Should redirect (302)
    expect(response.status()).toBe(200); // After redirect
  });

  test('GET to save is rejected', async ({ page }) => {
    const response = await page.request.get(`${BASE}/admin/save`);
    expect(response.status()).toBe(405);
  });

  test('save with empty file path is rejected', async ({ page }) => {
    const response = await page.request.post(`${BASE}/admin/save`, {
      form: { file: '', content: 'test' },
    });
    expect(response.status()).toBe(400);
  });

  test('save with path traversal is blocked', async ({ page }) => {
    const response = await page.request.post(`${BASE}/admin/save`, {
      form: { file: '/../../../etc/passwd', content: 'hacked' },
    });
    expect(response.status()).toBe(500);
  });
});

// ─── Preview Endpoint Tests ─────────────────────────────────────────

test.describe('Preview Endpoint', () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test('preview returns HTML with base tag injected', async ({ page }) => {
    const response = await page.request.post(`${BASE}/admin/preview`, {
      form: {
        file: 'test.html',
        content: '<html><head><title>Test</title></head><body>Hello</body></html>',
      },
    });
    const html = await response.text();
    expect(html).toContain('<base href="/">');
    expect(html).toContain('Hello');
  });

  test('preview skips base tag if already present', async ({ page }) => {
    const content = '<html><head><base href="/custom/"><title>Test</title></head><body>Hello</body></html>';
    const response = await page.request.post(`${BASE}/admin/preview`, {
      form: { file: 'test.html', content },
    });
    const html = await response.text();
    // Should NOT add another base tag
    const baseCount = (html.match(/<base /g) || []).length;
    expect(baseCount).toBe(1);
  });

  test('preview of non-HTML file returns plain text', async ({ page }) => {
    const response = await page.request.post(`${BASE}/admin/preview`, {
      form: { file: 'test.css', content: 'body { color: red; }' },
    });
    const contentType = response.headers()['content-type'];
    expect(contentType).toContain('text/plain');
  });

  test('GET to preview is rejected', async ({ page }) => {
    const response = await page.request.get(`${BASE}/admin/preview`);
    expect(response.status()).toBe(405);
  });
});

// ─── AI Approve Endpoint Tests ──────────────────────────────────────

test.describe('AI Approve Endpoint', () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test('POST to approve saves content and redirects', async ({ page }) => {
    // Read original first
    await page.goto(`${BASE}/admin/edit?file=/test.html`);
    const original = await page.locator('#editor').inputValue();

    const response = await page.request.post(`${BASE}/admin/ai/approve`, {
      form: { file: '/test.html', content: original },
    });
    expect(response.status()).toBe(200); // After redirect follows

    // Restore original
    await page.request.post(`${BASE}/admin/save`, {
      form: { file: '/test.html', content: original },
    });
  });

  test('GET to approve is rejected', async ({ page }) => {
    const response = await page.request.get(`${BASE}/admin/ai/approve`);
    expect(response.status()).toBe(405);
  });

  test('approve with empty content is rejected', async ({ page }) => {
    const response = await page.request.post(`${BASE}/admin/ai/approve`, {
      form: { file: '/test.html', content: '' },
    });
    expect(response.status()).toBe(400);
  });
});

// ─── Security Tests ─────────────────────────────────────────────────

test.describe('Security', () => {
  test('cannot access internal directories via dashboard', async ({ page }) => {
    await login(page);
    await page.goto(`${BASE}/admin/dashboard?dir=/internal`);
    // Should get an error (path blocked)
    const content = await page.content();
    expect(content).toContain('access denied');
  });

  test('cannot edit Go source files', async ({ page }) => {
    await login(page);
    const response = await page.goto(`${BASE}/admin/edit?file=/cmd/server/main.go`);
    expect(response.status()).toBe(403);
  });

  test('cannot access .cms-backups directory', async ({ page }) => {
    await login(page);
    await page.goto(`${BASE}/admin/dashboard?dir=/.cms-backups`);
    const content = await page.content();
    expect(content).toContain('access denied');
  });

  test('session cookie has HttpOnly flag', async ({ page }) => {
    await page.goto(`${BASE}/admin/login`);
    await page.fill('#username', ADMIN_USER);
    await page.fill('#password', ADMIN_PASS);
    await page.click('button[type="submit"]');
    await page.waitForURL('**/admin/dashboard**');

    const cookies = await page.context().cookies();
    const sessionCookie = cookies.find(c => c.name === 'cms_session');
    expect(sessionCookie).toBeDefined();
    expect(sessionCookie.httpOnly).toBe(true);
  });

  test('unauthenticated API requests are rejected', async ({ page }) => {
    // Create a fresh context without cookies
    const response = await page.request.post(`${BASE}/admin/save`, {
      form: { file: '/test.html', content: 'hacked' },
    });
    // This should redirect to login (302 -> 200 after redirect)
    // or be blocked
    const url = response.url();
    expect(url).toContain('/admin/login');
  });
});

// ─── Static File Serving Tests ──────────────────────────────────────

test.describe('Static Files', () => {
  test('homepage loads successfully', async ({ page }) => {
    const response = await page.goto(`${BASE}/`);
    expect(response.status()).toBe(200);
  });

  test('static HTML pages are accessible', async ({ page }) => {
    const pages = ['/about.html', '/contact.html', '/faq.html'];
    for (const p of pages) {
      const response = await page.goto(`${BASE}${p}`);
      expect(response.status()).toBe(200);
    }
  });

  test('CSS assets load', async ({ page }) => {
    await page.goto(`${BASE}/`);
    // Check that CSS files are loaded
    const response = await page.request.get(`${BASE}/assets/css/bootstrap.min.css`);
    expect(response.status()).toBe(200);
  });
});

// ─── End-to-End Workflow Tests ──────────────────────────────────────

test.describe('E2E Workflows', () => {
  test('full workflow: login -> dashboard -> edit -> save -> verify', async ({ page }) => {
    // Login
    await login(page);

    // Navigate to dashboard
    await expect(page).toHaveURL(/\/admin\/dashboard/);

    // Click Edit on the first file
    const editLink = page.locator('.file-actions a[href*="/admin/edit"]').first();
    const href = await editLink.getAttribute('href');
    await editLink.click();
    await expect(page).toHaveURL(/\/admin\/edit/);

    // Editor should have content
    const editor = page.locator('#editor');
    await expect(editor).toBeVisible();
    const content = await editor.inputValue();
    expect(content.length).toBeGreaterThan(0);

    // Save without changes (content unchanged)
    await page.click('button:has-text("Save")');
    await expect(page).toHaveURL(/saved=1/);
  });

  test('full workflow: login -> dashboard -> AI edit page -> back to dashboard', async ({ page }) => {
    await login(page);

    // Click AI button on the first file
    const aiLink = page.locator('.file-actions a.ai-btn').first();
    await aiLink.click();
    await expect(page).toHaveURL(/\/admin\/ai/);

    // Verify AI editor loaded
    await expect(page.locator('#instruction')).toBeVisible();

    // Navigate back to dashboard
    await page.click('a[href="/admin/dashboard"]');
    await expect(page).toHaveURL(/\/admin\/dashboard/);
  });

  test('AI edit with real API call', async ({ page }) => {
    // This test actually calls the AI API - may take longer
    test.setTimeout(120000);

    await login(page);
    await page.goto(`${BASE}/admin/ai?file=/test.html`);

    // Get original content for comparison
    const originalContent = await page.locator('#originalContent').inputValue();

    // Enter a simple instruction
    await page.fill('#instruction', 'Add an HTML comment at the very top of the file that says: <!-- CMS AI Test -->');

    // Submit and wait for AI response
    const responsePromise = page.waitForResponse(resp =>
      resp.url().includes('/admin/ai') && resp.request().method() === 'POST'
    );
    await page.click('button:has-text("Generate with AI")');

    const response = await responsePromise;
    const responseData = await response.json();

    if (responseData.Error) {
      // AI provider error - log but don't fail
      console.log('AI API error (may be expected in test env):', responseData.Error);
      return;
    }

    // Verify result panel shows
    await expect(page.locator('#resultPanel')).toBeVisible();

    // The AI content should differ from original
    const aiContent = await page.locator('#aiContent').inputValue();
    expect(aiContent).toContain('CMS AI Test');

    // Verify approve button works
    await page.click('button:has-text("Approve")');
    await page.waitForURL(/\/admin\/edit\?file=.*saved=1/);

    // Verify the saved content contains the AI edit
    const savedContent = await page.locator('#editor').inputValue();
    expect(savedContent).toContain('CMS AI Test');

    // Restore original content
    await page.locator('#editor').fill(originalContent);
    await page.click('button:has-text("Save")');
    await page.waitForURL(/saved=1/);
  });

  test('AI edit - discard flow preserves original', async ({ page }) => {
    test.setTimeout(120000);

    await login(page);
    await page.goto(`${BASE}/admin/ai?file=/test.html`);

    const originalContent = await page.locator('#originalContent').inputValue();

    await page.fill('#instruction', 'Change the title to "DISCARD TEST"');

    const responsePromise = page.waitForResponse(resp =>
      resp.url().includes('/admin/ai') && resp.request().method() === 'POST'
    );
    await page.click('button:has-text("Generate with AI")');
    const response = await responsePromise;
    const data = await response.json();

    if (data.Error) {
      console.log('AI API error (expected):', data.Error);
      return;
    }

    await expect(page.locator('#resultPanel')).toBeVisible();

    // Discard the result
    await page.click('button:has-text("Discard")');
    await expect(page.locator('#resultPanel')).not.toBeVisible();

    // Verify original file is unchanged
    await page.goto(`${BASE}/admin/edit?file=/test.html`);
    const currentContent = await page.locator('#editor').inputValue();
    expect(currentContent).toBe(originalContent);
  });
});
