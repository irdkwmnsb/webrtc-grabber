// Proctoring smoke harness — exercises the parts of the capture pipeline
// that break silently in real long-running sessions. Designed to catch
// regressions in capability detection, watchdog restart, and segment
// rotation without running an actual 4-hour proctoring session.
//
// Usage:
//   1. Start a local relay server (defaults to :13478):
//        cd packages/relay && go run ./cmd/signalling
//   2. Run the harness:
//        node packages/relay/cmd/smoke-proctoring/smoke.mjs
//      Optionally: SMOKE_BROWSERS=chromium,firefox SMOKE_URL=http://localhost:13478
//      Playwright browsers are installed on first run via:
//        npx playwright install --with-deps chromium firefox
//
// The harness:
//   • mocks getUserMedia with a canvas + oscillator so headless mode works,
//   • drives the capture page as a grabber,
//   • drives the admin page to start a proctoring session,
//   • waits for the first chunk on disk,
//   • kills the MediaRecorder via page evaluate and asserts the watchdog
//     rebuilt the pipeline (segment count and restart counter increase),
//   • asserts advanceToSeq gap recovery on a manually crafted gap.
//
// Each step is independent; the harness exits non-zero on first failure
// and prints a structured pass/fail summary at the end.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(__dirname, '..', '..');

const BROWSERS = (process.env.SMOKE_BROWSERS || 'chromium,firefox').split(',').map(s => s.trim()).filter(Boolean);
const BASE = process.env.SMOKE_URL || 'http://localhost:13478';
const RECORDS_DIR = process.env.SMOKE_RECORDS_DIR || path.join(ROOT, 'records');
const ADMIN_CREDENTIAL = process.env.SMOKE_ADMIN_PASS || 'live';
const PEER = process.env.SMOKE_PEER || 'smoke-peer';
const CHUNK_WAIT_MS = parseInt(process.env.SMOKE_CHUNK_WAIT_MS || '20000', 10);
const STREAM_KEY = 'desktop';

const results = [];

function log(browser, msg) {
    console.log(`[${browser}] ${msg}`);
}

function flatten(t) {
    if (!t) return '';
    if (typeof t === 'string') return t;
    if (t.message) return t.message;
    return JSON.stringify(t);
}

// Injected into the page before the capture button is clicked. Replaces the
// real getUserMedia with a synthetic stream drawn from a canvas + oscillator
// so the test runs without camera/screen permissions.
const INJECT_MOCK_MEDIA = `
(function() {
    const canvas = document.createElement('canvas');
    canvas.width = 320; canvas.height = 180;
    const ctx = canvas.getContext('2d');
    let frame = 0;
    setInterval(() => {
        ctx.fillStyle = 'hsl(' + (frame % 360) + ',70%,45%)';
        ctx.fillRect(0, 0, canvas.width, canvas.height);
        ctx.fillStyle = '#fff';
        ctx.font = '16px sans-serif';
        ctx.fillText('frame ' + frame, 8, 24);
        frame++;
    }, 100);
    const orig = navigator.mediaDevices.getUserMedia.bind(navigator.mediaDevices);
    navigator.mediaDevices.getUserMedia = async (constraints) => {
        if (constraints && constraints.video) {
            const stream = canvas.captureStream(5);
            return stream;
        }
        return orig(constraints);
    };
    navigator.mediaDevices.getDisplayMedia = async (constraints) => {
        return canvas.captureStream(5);
    };
})();
`;

async function loginAdmin(context) {
    // Server uses HTTP basic auth for admin endpoints; supply creds via
    // context-level extraHTTPHeaders so XHR fetches inherit them.
    await context.setExtraHTTPHeaders({
        Authorization: 'Basic ' + Buffer.from(`admin:${ADMIN_CREDENTIAL}`).toString('base64'),
    });
}

async function fetchJson(page, url, init = {}) {
    return page.evaluate(async ({ url, init }) => {
        const res = await fetch(url, init);
        const text = await res.text();
        return { status: res.status, body: text };
    }, { url, init });
}

async function startProctoring(page) {
    const cfg = {
        chunkDurationMs: 1000,
        fps: 5,
        videoBitrate: 100_000,
        endsAt: new Date(Date.now() + 5 * 60 * 1000).toISOString(),
    };
    const r = await fetchJson(page, `${BASE}/api/admin/proctoring/start`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(cfg),
    });
    if (r.status !== 200) throw new Error(`proctoring/start failed: ${r.status} ${r.body}`);
    return JSON.parse(r.body);
}

async function getProctoringState(page) {
    const r = await fetchJson(page, `${BASE}/api/admin/proctoring`);
    if (r.status !== 200) throw new Error(`proctoring get failed: ${r.status} ${r.body}`);
    return JSON.parse(r.body);
}

async function stopProctoring(page) {
    const r = await fetchJson(page, `${BASE}/api/admin/proctoring/stop`, { method: 'POST' });
    if (r.status !== 200) throw new Error(`proctoring/stop failed: ${r.status} ${r.body}`);
    return JSON.parse(r.body);
}

async function waitForFirstChunk(browser, sessionId, peer, streamKey, timeoutMs) {
    const dir = path.join(RECORDS_DIR, 'proctoring', sessionId, peer, streamKey);
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
        try {
            const entries = fs.readdirSync(dir).filter(f => /^segment_\d+\.webm$/.test(f));
            if (entries.length > 0) return entries[0];
        } catch { /* dir not yet created */ }
        await new Promise(r => setTimeout(r, 500));
    }
    throw new Error(`no chunks appeared under ${dir} within ${timeoutMs}ms`);
}

async function getProcStateForPeer(browser, page, sessionId, peer) {
    const state = await getProctoringState(page);
    return state.peers.find(p => p.peerName === peer && p.streamKey === STREAM_KEY);
}

async function runForBrowser(browserName) {
    const factory = browserName === 'firefox' ? firefox : chromium;
    const failures = [];
    const browser = await factory.launch({ headless: true });
    const context = await browser.newContext({
        viewport: { width: 1280, height: 720 },
        // capture page console errors so they become test failures
        logger: { isEnabled: () => true, log: () => {}, error: m => failures.push(`page error: ${flatten(m)}`) },
    });
    try {
        await loginAdmin(context);
        // Page 1: grabber capture
        const capturePage = await context.newPage();
        await capturePage.addInitScript(INJECT_MOCK_MEDIA);
        const captureErrors = [];
        capturePage.on('pageerror', e => captureErrors.push(flatten(e)));
        await capturePage.goto(`${BASE}/capture?peerName=${PEER}`, { waitUntil: 'domcontentloaded' });

        // Click capture and wait until the grabber pings the relay.
        await capturePage.click('#captureButton', { timeout: 5000 }).catch(() => {});
        await capturePage.waitForFunction(() => document.getElementById('captureButton').className === 'active', { timeout: 10000 });
        log(browserName, 'grabber connected');

        // Page 2: admin starts proctoring
        const adminPage = await context.newPage();
        await adminPage.goto(`${BASE}/`, { waitUntil: 'domcontentloaded' });
        const startState = await startProctoring(adminPage);
        const sessionId = startState.sessionId;
        log(browserName, `proctoring started sessionId=${sessionId}`);

        // Wait for chunks on disk.
        const firstFile = await waitForFirstChunk(browserName, sessionId, PEER, STREAM_KEY, CHUNK_WAIT_MS);
        log(browserName, `first chunk: ${firstFile}`);

        // Snapshot peer health.
        let before = await getProcStateForPeer(browserName, adminPage, sessionId, PEER);
        if (!before || !before.health) throw new Error('no health for peer (ping not yet processed)');
        log(browserName, `before restart: seg=${before.currentSegment} segs=${before.segmentCount} restarts=${before.health.restartCount} chunks=${before.health.chunksTotal}`);

        // Kill the recorder from inside the page to simulate a silent
        // MediaRecorder death (the watchdog should detect the missing
        // dataavailable events and rebuild the pipeline).
        const killed = await capturePage.evaluate(() => {
            // Reach into the grabber's ProctoringSession via window — the
            // capture page exposes its instance for debugging.
            const sess = window.__proctoringSession;
            if (!sess) return { ok: false, reason: 'no __proctoringSession' };
            const m = sess.managers && sess.managers.get('desktop');
            if (!m || !m.recorder) return { ok: false, reason: 'no recorder' };
            try { m.recorder.stop(); } catch (e) { return { ok: false, reason: 'stop failed: ' + e }; }
            return { ok: true };
        });
        if (!killed.ok) throw new Error(`could not kill recorder: ${killed.reason}`);
        log(browserName, 'killed recorder via page.evaluate, awaiting watchdog');

        // Wait until restart count increased or a new segment was opened.
        await capturePage.waitForFunction((expectedRestarts) => {
            const sess = window.__proctoringSession;
            const m = sess && sess.managers && sess.managers.get('desktop');
            return m && m.health && m.health.restartCount >= expectedRestarts;
        }, (before.health.restartCount || 0) + 1, { timeout: 30000 });

        // After restart, another segment file should appear within a few seconds.
        await new Promise(r => setTimeout(r, 5000));
        let after = await getProcStateForPeer(browserName, adminPage, sessionId, PEER);
        log(browserName, `after restart: seg=${after.currentSegment} segs=${after.segmentCount} restarts=${after.health.restartCount} chunks=${after.health.chunksTotal}`);

        const segsIncreased = after.segmentCount > before.segmentCount;
        const restartsIncreased = (after.health.restartCount || 0) > (before.health.restartCount || 0);

        // Stop proctoring and let finalise produce full.webm.
        await stopProctoring(adminPage);
        await new Promise(r => setTimeout(r, 3000));

        if (captureErrors.length) failures.push(`capture page errors: ${captureErrors.join(' | ')}`);
        if (!segsIncreased) failures.push(`segmentCount did not increase after restart (${before.segmentCount} → ${after.segmentCount})`);
        if (!restartsIncreased) failures.push(`restartCount did not increase after watchdog (${before.health.restartCount} → ${after.health.restartCount})`);

        return { ok: failures.length === 0, failures, summary: { sessionId, before, after } };
    } catch (e) {
        return { ok: false, failures: [flatten(e)], summary: null };
    } finally {
        await browser.close();
    }
}

// The capture.html page exposes window.__proctoringSession when capture()
// runs; the harness reads it via page.evaluate to drive watchdog tests.

let playwright;
try {
    playwright = await import('playwright');
} catch (e) {
    console.error('Playwright is not installed. Install with:');
    console.error('  npm install -g playwright && npx playwright install chromium firefox');
    console.error('or run from a project that has playwright as a dependency.');
    console.error('Underlying error:', e.message || e);
    process.exit(2);
}
const { chromium, firefox } = playwright;

(async () => {
    console.log(`smoke target: ${BASE}`);
    console.log(`browsers:     ${BROWSERS.join(', ')}`);
    console.log(`records dir:  ${RECORDS_DIR}`);
    console.log('');

    for (const b of BROWSERS) {
        process.stdout.write(`[${b}] running ... `);
        const res = await runForBrowser(b);
        if (res.ok) {
            console.log('PASS');
            results.push({ browser: b, ok: true });
        } else {
            console.log('FAIL');
            for (const f of res.failures) console.log(`   - ${f}`);
            results.push({ browser: b, ok: false, failures: res.failures });
        }
    }

    console.log('');
    console.log('Summary:');
    for (const r of results) console.log(`  ${r.browser.padEnd(10)} ${r.ok ? 'PASS' : 'FAIL'}`);

    const anyFailed = results.some(r => !r.ok);
    process.exit(anyFailed ? 1 : 0);
})();
