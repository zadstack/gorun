import {test, expect, type Page} from '@playwright/test';
import {randomUUID} from 'node:crypto';
import {csrf, setLocale} from '../lib/helpers.ts';

function gate() {
  let release!: () => void;
  const promise = new Promise<void>(resolve => {release = resolve;});
  return {promise, release};
}

async function board(page: Page) {
  await page.goto('/admin/canvases');
  await setLocale(page, 'en');
  const title = 'E2E editing race ' + randomUUID();
  const created = await page.request.post('/admin/canvases', {form: {_csrf: await csrf(page), title}, maxRedirects: 0});
  expect(created.status()).toBe(303);
  const path = created.headers().location;
  await page.goto(path);
  await expect(page.locator('.canvas-state')).toHaveAttribute('data-state', 'saved');
  return {path, id: path.split('/').pop()!, token: await csrf(page), title};
}

async function removeBoard(page: Page, fixture: {path: string; token: string}) {
  const response = await page.request.get(fixture.path + '/document');
  if (!response.ok()) return;
  const saved = await response.json();
  await page.request.delete(fixture.path, {headers: {'X-CSRF-Token': fixture.token}, data: {version: saved.version}});
}

async function addNote(page: Page, title: string, body: string) {
  await page.locator('#canvas-kind').selectOption('note');
  await page.locator('[data-action=add]').click();
  const form = page.locator('#canvas-card-form');
  await form.locator('[name=title]').fill(title);
  await form.locator('[name=body]').fill(body);
  await form.locator('[type=submit]').click();
  await expect(page.locator('.canvas-card[data-kind=note]')).toHaveCount(1);
}

async function drafts(page: Page, boardID: string) {
  return page.evaluate(board => Object.keys(localStorage).filter(key => key.startsWith('gorun:canvas-draft:v1:')).flatMap(key => {
    try {
      const value = JSON.parse(localStorage.getItem(key)!);
      return value.scope?.board === board ? [{key, ...value}] : [];
    } catch {return [];}
  }), boardID);
}

test('a delayed save acknowledgement rebases but cannot clear a newer local edit across reload', async ({page}) => {
  const fixture = await board(page), endpoint = fixture.path + '/document', pattern = '**' + endpoint;
  const firstStored = gate(), releaseAcknowledgement = gate();
  const first = 'First version accepted by the server.', newest = 'Newer private writing typed while the first acknowledgement is delayed.';
  let posts = 0, firstStatus = 0;
  await page.route(pattern, async route => {
    if (route.request().method() !== 'POST') return route.continue();
    posts++;
    if (posts !== 1) return route.abort('internetdisconnected');
    const response = await route.fetch();
    firstStatus = response.status();
    firstStored.release();
    await releaseAcknowledgement.promise;
    await route.fulfill({response});
  });
  try {
    await addNote(page, 'Acknowledgement race note', first);
    await firstStored.promise;
    expect(firstStatus).toBe(200);
    // The real API has already committed the first version; only its browser
    // acknowledgement is held. Later browser writes simulate a disconnected link.
    const accepted = await (await page.request.get(endpoint)).json();
    expect(accepted.cards[0].body).toBe(first);
    const card = page.locator('.canvas-card[data-kind=note]');
    await card.hover();
    await card.locator('.canvas-card-actions>button').first().click();
    const form = page.locator('#canvas-card-form');
    await form.locator('[name=body]').fill(newest);
    await form.locator('[type=submit]').click();
    releaseAcknowledgement.release();
    await expect(page.locator('.canvas-state')).toHaveAttribute('data-state', 'error');
    const pending = await drafts(page, fixture.id);
    expect(pending).toHaveLength(1);
    expect(pending[0].document.cards[0].body).toBe(newest);
    expect(pending[0].baseVersion).toBe(accepted.version);
    const beforeReload = posts;
    page.once('dialog', dialog => dialog.accept());
    await page.reload();
    const recovery = page.locator('[data-canvas-recovery]');
    await expect(recovery).toBeVisible();
    await expect(page.locator('.canvas-note-text')).toHaveText(first);
    expect((await drafts(page, fixture.id))[0].document.cards[0].body).toBe(newest);
    expect(posts).toBe(beforeReload); // Recovery never replays a save automatically.
    await page.unroute(pattern);
    await recovery.locator('[data-recovery-restore]').click();
    await expect(page.locator('.canvas-note-text')).toHaveText(newest);
    await expect(recovery).toBeHidden();
    expect((await (await page.request.get(endpoint)).json()).cards[0].body).toBe(newest);
  } finally {
    releaseAcknowledgement.release();
    await page.unroute(pattern);
    await removeBoard(page, fixture);
  }
});

test('an Undo freshness read cannot apply its inverse after a newer local edit', async ({page}) => {
  const fixture = await board(page), endpoint = fixture.path + '/document', pattern = '**' + endpoint;
  const freshRead = gate(), releaseFreshRead = gate();
  const newestTitle = 'New title while Undo checks the server ' + randomUUID();
  const attemptedWrites: any[] = [];
  let holdNextGet = true;
  try {
    await addNote(page, 'Keep this note', 'The note must survive the obsolete Undo.');
    await expect(page.locator('.canvas-state')).toHaveAttribute('data-state', 'saved');
    await page.route(pattern, async route => {
      if (route.request().method() === 'POST') {
        attemptedWrites.push(route.request().postDataJSON());
        return route.abort('internetdisconnected');
      }
      if (!holdNextGet) return route.continue();
      holdNextGet = false;
      const response = await route.fetch();
      freshRead.release();
      await releaseFreshRead.promise;
      await route.fulfill({response});
    });
    await page.locator('[data-action=undo]').click();
    await freshRead.promise;
    await page.locator('#canvas-title').fill(newestTitle);
    expect((await drafts(page, fixture.id))[0].document.title).toBe(newestTitle);
    releaseFreshRead.release();
    await expect(page.locator('[data-action=undo]')).toBeEnabled();
    await expect(page.locator('.canvas-card[data-kind=note]')).toHaveCount(1);
    await expect(page.locator('#canvas-title')).toHaveValue(newestTitle);
    await expect(page.locator('.canvas-state')).toHaveAttribute('data-state', 'error');
    expect(attemptedWrites.length).toBeGreaterThan(0);
    expect(attemptedWrites.every(write => write.title === newestTitle && write.cards.length === 1)).toBe(true);
    expect((await drafts(page, fixture.id))[0].document.cards[0].body).toBe('The note must survive the obsolete Undo.');
    const saved = await (await page.request.get(endpoint)).json();
    expect(saved.title).toBe(fixture.title);
    expect(saved.cards).toHaveLength(1);
  } finally {
    releaseFreshRead.release();
    await page.unroute(pattern);
    await removeBoard(page, fixture);
  }
});

test('same-page conflict Reload preserves the old draft when a new local edit starts', async ({page}) => {
  const fixture = await board(page), endpoint = fixture.path + '/document', pattern = '**' + endpoint;
  const savedBody = 'This body was saved before the remote update.';
  const conflictBody = 'Keep this exact writing from before conflict Reload.';
  const remoteTitle = 'Remote title before same-page Reload ' + randomUUID();
  const nextTitle = 'New writing after conflict Reload ' + randomUUID();
  try {
    await addNote(page, 'Conflict Reload note', savedBody);
    await expect(page.locator('.canvas-state')).toHaveAttribute('data-state', 'saved');
    const saved = await (await page.request.get(endpoint)).json();
    const remoteResponse = await page.request.post(endpoint, {
      headers: {'X-CSRF-Token': fixture.token},
      data: {version: saved.version, title: remoteTitle, cards: saved.cards, edges: saved.edges},
    });
    expect(remoteResponse.status()).toBe(200);
    const remote = await remoteResponse.json();
    const card = page.locator('.canvas-card[data-kind=note]');
    await card.hover();
    await card.locator('.canvas-card-actions>button').first().click();
    const form = page.locator('#canvas-card-form');
    await form.locator('[name=body]').fill(conflictBody);
    await form.locator('[type=submit]').click();
    await expect(page.locator('.canvas-state')).toHaveAttribute('data-state', 'conflict');
    const beforeReload = await drafts(page, fixture.id);
    expect(beforeReload).toHaveLength(1);
    const protectedDraft = beforeReload[0];
    expect(protectedDraft.baseVersion).toBe(saved.version);
    expect(protectedDraft.document.cards[0].body).toBe(conflictBody);

    // This button fetches the server document inside the existing editor;
    // it must release the writer without discarding its protected draft.
    page.once('dialog', dialog => dialog.accept());
    await page.locator('[data-action=reload]').click();
    await expect(page.locator('.canvas-state')).toHaveAttribute('data-state', 'saved');
    await expect(page.locator('#canvas-title')).toHaveValue(remoteTitle);
    await expect(page.locator('.canvas-note-text')).toHaveText(savedBody);
    await expect(page.locator('[data-canvas-recovery]')).toBeVisible();
    expect(await drafts(page, fixture.id)).toEqual([protectedDraft]);

    await page.route(pattern, route => route.request().method() === 'POST' ? route.abort('internetdisconnected') : route.continue());
    await page.locator('#canvas-title').fill(nextTitle);
    await expect(page.locator('.canvas-state')).toHaveAttribute('data-state', 'error');
    const afterEdit = await drafts(page, fixture.id);
    expect(afterEdit).toHaveLength(2);
    expect(afterEdit.find(entry => entry.key === protectedDraft.key)).toEqual(protectedDraft);
    const nextDraft = afterEdit.find(entry => entry.document.title === nextTitle);
    expect(nextDraft).toBeDefined();
    expect(nextDraft.key).not.toBe(protectedDraft.key);
    expect(nextDraft.writer).not.toBe(protectedDraft.writer);
    expect(nextDraft.baseVersion).toBe(remote.version);
    expect(nextDraft.document.cards[0].body).toBe(savedBody);
    expect((await (await page.request.get(endpoint)).json()).title).toBe(remoteTitle);
  } finally {
    await page.unroute(pattern);
    await removeBoard(page, fixture);
  }
});

test('typing during a pending conflict Reload keeps the newer local document and draft', async ({page}) => {
  const fixture = await board(page), endpoint = fixture.path + '/document', pattern = '**' + endpoint;
  const reloadRead = gate(), releaseReload = gate();
  const localBody = 'Keep this unsaved local body while Reload is waiting.';
  const newestTitle = 'Typed while conflict Reload waits ' + randomUUID();
  const remoteCard = {id: randomUUID(), kind: 'note', title: 'Remote-only note', body: 'This card must remain on the server until explicit reload or recovery.', x: 720, y: 24};
  let browserWrites = 0;
  try {
    await addNote(page, 'Pending Reload note', 'Original saved note body.');
    await expect(page.locator('.canvas-state')).toHaveAttribute('data-state', 'saved');
    const saved = await (await page.request.get(endpoint)).json();
    const remoteResponse = await page.request.post(endpoint, {
      headers: {'X-CSRF-Token': fixture.token},
      data: {version: saved.version, title: saved.title, cards: [...saved.cards, remoteCard], edges: saved.edges},
    });
    expect(remoteResponse.status()).toBe(200);
    const remote = await remoteResponse.json();
    const card = page.locator('.canvas-card[data-kind=note]');
    await card.hover();
    await card.locator('.canvas-card-actions>button').first().click();
    const form = page.locator('#canvas-card-form');
    await form.locator('[name=body]').fill(localBody);
    await form.locator('[type=submit]').click();
    await expect(page.locator('.canvas-state')).toHaveAttribute('data-state', 'conflict');
    const beforeReload = (await drafts(page, fixture.id))[0];

    await page.route(pattern, async route => {
      if (route.request().method() === 'POST') {
        browserWrites++;
        return route.continue();
      }
      const response = await route.fetch();
      reloadRead.release();
      await releaseReload.promise;
      return route.fulfill({response});
    });
    const reloadResponse = page.waitForResponse(response => new URL(response.url()).pathname === endpoint && response.request().method() === 'GET');
    page.once('dialog', dialog => dialog.accept());
    await page.locator('[data-action=reload]').click();
    await reloadRead.promise;
    await expect(page.locator('.canvas-state')).toHaveAttribute('data-state', 'loading');
    await page.locator('#canvas-title').fill(newestTitle);
    const newerDraft = (await drafts(page, fixture.id))[0];
    expect(newerDraft.key).toBe(beforeReload.key);
    expect(newerDraft.revision).not.toBe(beforeReload.revision);
    expect(newerDraft.document.title).toBe(newestTitle);
    expect(newerDraft.document.cards[0].body).toBe(localBody);
    expect(newerDraft.baseVersion).toBe(saved.version);

    releaseReload.release();
    const response = await reloadResponse;
    expect(response.status()).toBe(200);
    expect((await response.json()).cards).toHaveLength(2);
    // Let the released fetch and its document-adoption continuation settle;
    // an immediate unchanged-value assertion could run before the stale load.
    await page.waitForLoadState('networkidle');
    await expect(page.locator('#canvas-title')).toHaveValue(newestTitle);
    await expect(page.locator('.canvas-note-text')).toHaveText(localBody);
    await expect(page.locator('.canvas-card[data-kind=note]')).toHaveCount(1);
    await expect(page.locator('.canvas-state')).toHaveAttribute('data-state', 'conflict');
    expect(await drafts(page, fixture.id)).toEqual([newerDraft]);
    expect(browserWrites).toBe(0);
    const original = await (await page.request.get(endpoint)).json();
    expect(original.version).toBe(remote.version);
    expect(original.title).toBe(saved.title);
    expect(original.cards).toHaveLength(2);
    expect(original.cards[0].body).toBe('Original saved note body.');
  } finally {
    releaseReload.release();
    await page.unroute(pattern);
    await removeBoard(page, fixture);
  }
});

test('a failed Undo freshness read re-arms autosave for pending local changes', async ({page}) => {
  await page.clock.install();
  const fixture = await board(page), endpoint = fixture.path + '/document', pattern = '**' + endpoint;
  const nextTitle = 'Pending title survives failed Undo ' + randomUUID();
  const writes: any[] = [];
  let freshnessReads = 0;
  try {
    await addNote(page, 'Keep this note after failed Undo', 'The failed freshness read must not undo this note.');
    await expect(page.locator('.canvas-state')).toHaveAttribute('data-state', 'saved');
    const saved = await (await page.request.get(endpoint)).json();
    await page.route(pattern, route => {
      if (route.request().method() === 'POST') {
        writes.push(route.request().postDataJSON());
        return route.continue();
      }
      freshnessReads++;
      return route.fulfill({status: 503, contentType: 'application/json', body: JSON.stringify({error: 'Synthetic freshness read failure'})});
    });
    // Keep the pending autosave from firing before Undo clears its timer.
    // Only the history failure's finally block can schedule the later save.
    await page.clock.pauseAt(await page.evaluate(() => Date.now()) + 1000);
    await page.locator('#canvas-title').fill(nextTitle);
    await expect(page.locator('.canvas-state')).toHaveAttribute('data-state', 'unsaved');
    await page.locator('[data-action=undo]').click();
    await expect(page.locator('[data-editing-message]')).toContainText('Could not change edit history');
    await expect(page.locator('[data-action=undo]')).toBeEnabled();
    expect(freshnessReads).toBe(1);
    expect(writes).toHaveLength(0);
    await expect(page.locator('#canvas-title')).toHaveValue(nextTitle);
    await expect(page.locator('.canvas-card[data-kind=note]')).toHaveCount(1);
    expect((await drafts(page, fixture.id))[0].document.title).toBe(nextTitle);
    await page.clock.runFor(899);
    expect(writes).toHaveLength(0);
    await page.clock.runFor(2);
    await expect(page.locator('.canvas-state')).toHaveAttribute('data-state', 'saved');
    expect(writes).toHaveLength(1);
    expect(writes[0].version).toBe(saved.version);
    expect(writes[0].title).toBe(nextTitle);
    expect(writes[0].cards).toHaveLength(1);
    const accepted = await (await page.request.get(endpoint)).json();
    expect(accepted.title).toBe(nextTitle);
    expect(accepted.cards[0].body).toBe('The failed freshness read must not undo this note.');
    expect(await drafts(page, fixture.id)).toHaveLength(0);
  } finally {
    await page.unroute(pattern);
    await removeBoard(page, fixture);
    await page.clock.resume();
  }
});

test('editing after an Undo conflict keeps the old draft baseline and offers copy-only recovery', async ({page}) => {
  const fixture = await board(page), endpoint = fixture.path + '/document', pattern = '**' + endpoint;
  const localTitle = 'Private title after Undo conflict ' + randomUUID();
  const remoteCard = {id: randomUUID(), kind: 'note', title: 'Remote second note', body: 'Keep this remotely added writing in the original board.', x: 720, y: 24};
  let browserWrites = 0;
  try {
    await addNote(page, 'Saved local note', 'This note belongs to the older local document.');
    await expect(page.locator('.canvas-state')).toHaveAttribute('data-state', 'saved');
    expect(await drafts(page, fixture.id)).toHaveLength(0);
    const saved = await (await page.request.get(endpoint)).json();
    const remoteResponse = await page.request.post(endpoint, {
      headers: {'X-CSRF-Token': fixture.token},
      data: {version: saved.version, title: saved.title, cards: [...saved.cards, remoteCard], edges: saved.edges},
    });
    expect(remoteResponse.status()).toBe(200);
    const remote = await remoteResponse.json();
    expect(remote.version).toBeGreaterThan(saved.version);
    await page.route(pattern, route => {
      if (route.request().method() === 'POST') browserWrites++;
      return route.continue();
    });
    await page.locator('[data-action=undo]').click();
    await expect(page.locator('.canvas-state')).toHaveAttribute('data-state', 'conflict');
    await expect(page.locator('.canvas-card[data-kind=note]')).toHaveCount(1);
    await page.locator('#canvas-title').fill(localTitle);
    const pending = await drafts(page, fixture.id);
    expect(pending).toHaveLength(1);
    expect(pending[0].baseVersion).toBe(saved.version);
    expect(pending[0].baseVersion).not.toBe(remote.version);
    expect(pending[0].document.title).toBe(localTitle);
    expect(pending[0].document.cards).toHaveLength(1);

    page.once('dialog', dialog => dialog.accept());
    await page.reload();
    await expect(page.locator('.canvas-state')).toHaveAttribute('data-state', 'saved');
    await expect(page.locator('.canvas-card[data-kind=note]')).toHaveCount(2);
    await expect(page.locator('#canvas-title')).toHaveValue(saved.title);
    const recovery = page.locator('[data-canvas-recovery]');
    await expect(recovery).toBeVisible();
    const item = recovery.locator('.canvas-recovery-item').filter({hasText: localTitle});
    await expect(item).toHaveCount(1);
    await expect(item.locator('[data-recovery-copy]')).toBeVisible();
    await expect(item.locator('[data-recovery-copy]')).toBeEnabled();
    await expect(item.locator('[data-recovery-restore]')).toHaveCount(0);
    expect(await drafts(page, fixture.id)).toEqual(pending);
    expect(browserWrites).toBe(0);
    const original = await (await page.request.get(endpoint)).json();
    expect(original.version).toBe(remote.version);
    expect(original.title).toBe(saved.title);
    expect(original.cards).toHaveLength(2);
    expect(original.cards.find((card: {id: string}) => card.id === remoteCard.id)?.body).toBe(remoteCard.body);
  } finally {
    await page.unroute(pattern);
    await removeBoard(page, fixture);
  }
});

test('two disconnected tabs keep independent drafts and discarding one preserves the other', async ({page, context}) => {
  const fixture = await board(page), endpoint = fixture.path + '/document', pattern = '**' + endpoint;
  const other = await context.newPage();
  const firstTitle = 'First tab local copy ' + randomUUID(), secondTitle = 'Second tab local copy ' + randomUUID();
  const firstBody = 'Only the first tab may discard this writing.', secondBody = 'The second tab keeps this exact private writing.';
  let writes = 0;
  const disconnectWrites = async (route: import('@playwright/test').Route) => {
    if (route.request().method() !== 'POST') return route.continue();
    writes++;
    return route.abort('internetdisconnected');
  };
  try {
    await other.goto(fixture.path);
    await expect(other.locator('.canvas-state')).toHaveAttribute('data-state', 'saved');
    await page.route(pattern, disconnectWrites);
    await other.route(pattern, disconnectWrites);
    await addNote(page, 'First tab note', firstBody);
    await page.locator('#canvas-title').fill(firstTitle);
    await addNote(other, 'Second tab note', secondBody);
    await other.locator('#canvas-title').fill(secondTitle);
    await expect(page.locator('.canvas-state')).toHaveAttribute('data-state', 'error');
    await expect(other.locator('.canvas-state')).toHaveAttribute('data-state', 'error');
    await expect.poll(async () => (await drafts(page, fixture.id)).length).toBe(2);
    const before = await drafts(page, fixture.id);
    expect(new Set(before.map(entry => entry.writer)).size).toBe(2);
    const secondDraft = before.find(entry => entry.document.title === secondTitle);
    expect(secondDraft.document.cards[0].body).toBe(secondBody);
    const firstRecovery = page.locator('.canvas-recovery-item').filter({hasText: firstTitle});
    await expect(firstRecovery).toHaveCount(1);
    page.once('dialog', dialog => dialog.accept());
    await firstRecovery.locator('[data-recovery-discard]').click();
    await expect.poll(async () => (await drafts(page, fixture.id)).length).toBe(1);
    const retained = (await drafts(page, fixture.id))[0];
    expect(retained.key).toBe(secondDraft.key);
    expect(retained.revision).toBe(secondDraft.revision);
    expect(retained.document.cards[0].body).toBe(secondBody);
    await expect(other.locator('#canvas-title')).toHaveValue(secondTitle);
    await expect(other.locator('.canvas-note-text')).toHaveText(secondBody);
    const beforeReload = writes;
    other.once('dialog', dialog => dialog.accept());
    await other.reload();
    await expect(other.locator('[data-canvas-recovery]')).toBeVisible();
    await expect(other.locator('.canvas-recovery-item').filter({hasText: secondTitle})).toHaveCount(1);
    await expect(other.locator('.canvas-card')).toHaveCount(0);
    expect((await drafts(other, fixture.id))[0].document.cards[0].body).toBe(secondBody);
    expect(writes).toBe(beforeReload);
    expect((await (await page.request.get(endpoint)).json()).cards).toHaveLength(0);
  } finally {
    await page.unroute(pattern);
    await other.unroute(pattern);
    await other.close();
    await removeBoard(page, fixture);
  }
});

test('acknowledged result deletion disables Undo and never submits the immutable result again', async ({page}) => {
  const fixture = await board(page), endpoint = fixture.path + '/document', pattern = '**' + endpoint;
  const resultID = randomUUID();
  let injectResult = true;
  const writes: any[] = [];
  // A synthetic read fixture represents a private result delivered by a
  // completed run. Every mutation still uses the normal, real canvas API.
  // No external model, private media or production database is accessed.
  await page.route(pattern, async route => {
    if (route.request().method() === 'POST') {
      writes.push(route.request().postDataJSON());
      return route.continue();
    }
    if (!injectResult) return route.continue();
    injectResult = false;
    const response = await route.fetch(), document = await response.json();
    document.cards = [{id: resultID, kind: 'result', run_id: fixture.id, title: 'Synthetic finished result', body: 'Synthetic private result text.', x: 24, y: 24}];
    await route.fulfill({response, json: document});
  });
  try {
    await page.reload();
    const result = page.locator(`[data-card-id="${resultID}"]`);
    await expect(result).toBeVisible();
    let warning = '';
    page.once('dialog', async dialog => {warning = dialog.message(); await dialog.accept();});
    await result.hover();
    const acknowledgement = page.waitForResponse(response => new URL(response.url()).pathname === endpoint && response.request().method() === 'POST');
    await result.locator('[data-remove]').click();
    expect(warning).toContain('Once this deletion is saved');
    expect((await acknowledgement).status()).toBe(200);
    await expect(page.locator('.canvas-state')).toHaveAttribute('data-state', 'saved');
    await expect(page.locator('[data-action=undo]')).toBeDisabled();
    await expect(page.locator('[data-editing-message]')).toContainText('run identity cannot be restored');
    await page.locator('.canvas-viewport').focus();
    await page.keyboard.press('Control+z');
    await page.waitForLoadState('networkidle');
    await expect(page.locator('.canvas-card[data-kind=result]')).toHaveCount(0);
    expect(writes).toHaveLength(1);
    expect(writes[0].cards).toHaveLength(0);
    const saved = await (await page.request.get(endpoint)).json();
    expect(saved.cards).toHaveLength(0);
  } finally {
    await page.unroute(pattern);
    await removeBoard(page, fixture);
  }
});
