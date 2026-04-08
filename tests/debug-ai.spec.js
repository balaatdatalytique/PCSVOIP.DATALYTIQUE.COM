const { test, expect } = require('@playwright/test');

const BASE = 'http://localhost:9080';

async function login(page) {
  await page.goto(`${BASE}/admin/login`);
  await page.fill('#username', 'admin');
  await page.fill('#password', 'admin123');
  await page.click('button[type="submit"]');
  await page.waitForURL('**/admin/dashboard**');
}

test('Debug: AI edit full trace on clients.html', async ({ page }) => {
  test.setTimeout(120000);
  await login(page);

  // Go to AI editor for clients.html
  await page.goto(`${BASE}/admin/ai?file=/clients.html`);

  // Capture original content from the page
  const originalContent = await page.locator('#originalContent').inputValue();
  console.log('Original content length:', originalContent.length);
  console.log('Original has gallery-card border:', originalContent.includes('gallery-card'));

  // Check what border style currently exists
  const borderMatch = originalContent.match(/\.gallery-card[^}]*border[^;]*/s);
  console.log('Current gallery-card border CSS:', borderMatch ? borderMatch[0].substring(0, 200) : 'NOT FOUND');

  // Enter instruction
  await page.fill('#instruction', 'Change the border of client logo images to 1px solid black');

  // Listen to the network response
  const responsePromise = page.waitForResponse(resp =>
    resp.url().includes('/admin/ai') && resp.request().method() === 'POST'
  );

  await page.click('button:has-text("Generate with AI")');

  const response = await responsePromise;
  const status = response.status();
  const data = await response.json();

  console.log('Response status:', status);
  console.log('Response has Error:', !!data.Error);
  if (data.Error) {
    console.log('ERROR:', data.Error);
    return;
  }
  console.log('Response Result length:', data.Result?.length);

  // Check if result panel becomes visible
  const resultPanel = page.locator('#resultPanel');
  const isVisible = await resultPanel.isVisible();
  console.log('Result panel visible:', isVisible);

  // Check aiContent textarea value
  const aiContent = await page.locator('#aiContent').inputValue();
  console.log('AI content textarea length:', aiContent.length);
  console.log('AI content matches response:', aiContent === data.Result);

  // Check the diff - what changed?
  if (aiContent && originalContent) {
    const origLines = originalContent.split('\n');
    const aiLines = aiContent.split('\n');
    let diffs = 0;
    for (let i = 0; i < Math.max(origLines.length, aiLines.length); i++) {
      if (origLines[i] !== aiLines[i]) {
        if (diffs < 5) {
          console.log(`DIFF at line ${i+1}:`);
          console.log(`  ORIG: ${(origLines[i] || '').substring(0, 120)}`);
          console.log(`  NEW:  ${(aiLines[i] || '').substring(0, 120)}`);
        }
        diffs++;
      }
    }
    console.log('Total diff lines:', diffs);
  }

  // Now click Approve & Save
  if (isVisible && aiContent.length > 0) {
    console.log('--- Clicking Approve & Save ---');

    // Check the hidden field BEFORE submit
    const approveContentBefore = await page.locator('#approveContent').inputValue();
    console.log('approveContent hidden field BEFORE submit:', approveContentBefore.length, 'chars');

    // Listen for the approve request
    const approveResponsePromise = page.waitForResponse(resp =>
      resp.url().includes('/admin/ai/approve')
    );

    await page.click('button:has-text("Approve")');

    const approveResponse = await approveResponsePromise;
    console.log('Approve response status:', approveResponse.status());
    console.log('Approve response URL:', approveResponse.url());

    // Should redirect to editor with saved=1
    await page.waitForURL(/\/admin\/edit/, { timeout: 5000 });
    console.log('Final URL:', page.url());

    // Now verify the file was actually saved
    const savedContent = await page.locator('#editor').inputValue();
    console.log('Saved content length:', savedContent.length);
    console.log('Saved matches AI result:', savedContent === aiContent);

    // Check if the change is in the saved content
    const savedBorderMatch = savedContent.match(/border[^;]*1px[^;]*/);
    console.log('Saved content has 1px border:', !!savedBorderMatch);
    if (savedBorderMatch) {
      console.log('Border match:', savedBorderMatch[0]);
    }

    // Restore original
    await page.locator('#editor').fill(originalContent);
    await page.click('button:has-text("Save")');
    await page.waitForURL(/saved=1/);
    console.log('Original content restored');
  }
});

test('Debug: Multiple AI edits on test.html', async ({ page }) => {
  test.setTimeout(120000);
  await login(page);

  // Save original for restore
  await page.goto(`${BASE}/admin/edit?file=/test.html`);
  const originalContent = await page.locator('#editor').inputValue();

  const edits = [
    'Change the map height from 500px to 600px',
    'Change the page title to "Interactive Map Demo"',
  ];

  for (const instruction of edits) {
    console.log(`\n=== Testing: "${instruction}" ===`);
    await page.goto(`${BASE}/admin/ai?file=/test.html`);
    await page.fill('#instruction', instruction);

    const responsePromise = page.waitForResponse(resp =>
      resp.url().includes('/admin/ai') && resp.request().method() === 'POST'
    );
    await page.click('button:has-text("Generate with AI")');
    const response = await responsePromise;
    const data = await response.json();

    if (data.Error) {
      console.log('ERROR:', data.Error);
      continue;
    }

    console.log('AI returned result, length:', data.Result.length);
    const visible = await page.locator('#resultPanel').isVisible();
    console.log('Result panel visible:', visible);

    if (visible) {
      // Approve
      const approvePromise = page.waitForResponse(r => r.url().includes('/admin/ai/approve'));
      await page.click('button:has-text("Approve")');
      const approveResp = await approvePromise;
      console.log('Approve status:', approveResp.status());
      await page.waitForURL(/\/admin\/edit/, { timeout: 5000 });

      // Verify saved
      const saved = await page.locator('#editor').inputValue();
      if (instruction.includes('600px')) {
        console.log('Has 600px:', saved.includes('600px'));
      }
      if (instruction.includes('Interactive Map')) {
        console.log('Has new title:', saved.includes('Interactive Map Demo'));
      }
    }
  }

  // Restore original
  await page.goto(`${BASE}/admin/edit?file=/test.html`);
  await page.locator('#editor').fill(originalContent);
  await page.click('button:has-text("Save")');
  await page.waitForURL(/saved=1/);
  console.log('\nOriginal test.html restored');
});
