// node cdp.mjs <port> <expr-file> — evaluate the file's function expression in the first page target of a
// Chrome DevTools endpoint (an Android WebView after `adb forward tcp:<port> localabstract:webview_devtools_remote_<pid>`)
// and print the JSON result. The app must be in the foreground: a backgrounded WebView's renderer is paused
// and would never answer, hence the timeout.
import fs from 'node:fs';
const [port, file] = process.argv.slice(2);
if (!port || !file) { console.error('usage: node cdp.mjs <port> <expr-file>'); process.exit(2); }
const fail = (msg) => { console.error('cdp.mjs: ' + msg); process.exit(1); };
const timer = setTimeout(() => fail('no answer in 15 s (is the app in the foreground?)'), 15000);
let targets;
try { targets = await (await fetch(`http://localhost:${port}/json`)).json(); } catch (e) { fail('no DevTools endpoint on port ' + port); }
const t = targets.find(x => x.type === 'page');
if (!t) fail('no page target (the WebView has no document open)');
const ws = new WebSocket(t.webSocketDebuggerUrl);
ws.onerror = () => fail('websocket error');
ws.onmessage = (m) => {
  const d = JSON.parse(m.data);
  if (d.id !== 1) return;
  clearTimeout(timer);
  const r = d.result || {};
  if (r.exceptionDetails) fail(JSON.stringify(r.exceptionDetails).slice(0, 600));
  console.log(JSON.stringify(r.result && r.result.value, null, 1));
  ws.close();
};
ws.onopen = () => ws.send(JSON.stringify({id: 1, method: 'Runtime.evaluate',
  params: {expression: '(' + fs.readFileSync(file, 'utf8') + ')()', returnByValue: true, awaitPromise: true}}));
