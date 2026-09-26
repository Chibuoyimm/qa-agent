import { createServer } from 'node:http';
import { randomUUID } from 'node:crypto';

export const orders = Object.freeze([
  { id: 'ord-paid', state: 'paid', amount: 150000 },
  { id: 'ord-refund', state: 'refunded', amount: 10000 },
  { id: 'ord-cancelled', state: 'cancelled', amount: 90000 },
]);

const expectedEmail = process.env.QA_TEST_EMAIL ?? 'demo@example.test';
const expectedPassword = process.env.QA_TEST_PASSWORD ?? 'pass1234';
const sessions = new Set();

function page(title, body) {
  return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>${title}</title><style>body{font:16px system-ui;max-width:760px;margin:3rem auto;padding:0 1rem;color:#18212c}input,button{font:inherit;padding:.6rem;margin:.25rem 0}label{display:block;margin:1rem 0}table{border-collapse:collapse;width:100%}td,th{padding:.6rem;border-bottom:1px solid #ddd;text-align:left}.error{color:#9b1c1c}</style></head><body>${body}</body></html>`;
}

const loginPage = page('Sample login', `<h1>Acme Metrics — synthetic sample</h1><p>Sign in with the documented test account.</p><form method="post" action="/login"><label>Email<br><input data-testid="login-email" name="email" type="email" autocomplete="username" required></label><label>Password<br><input data-testid="login-password" name="password" type="password" autocomplete="current-password" required></label><button data-testid="login-submit" type="submit">Sign in</button></form>`);
const dashboardPage = page('Sample dashboard', `<h1>Acme Metrics dashboard</h1><p>Net revenue: <strong data-testid="dashboard-revenue">Loading</strong></p><p data-testid="dashboard-orders">Loading orders</p><table><thead><tr><th>Order</th><th>Status</th><th>Amount</th></tr></thead><tbody id="orders"></tbody></table><form method="post" action="/signout"><button data-testid="signout" type="submit">Sign out</button></form><script>fetch('/api/dashboard').then(r => { if (!r.ok) throw Error('failed'); return r.json(); }).then(data => { document.querySelector('[data-testid="dashboard-revenue"]').textContent = String(data.net_revenue); document.querySelector('[data-testid="dashboard-orders"]').textContent = String(data.orders.length) + ' orders'; document.getElementById('orders').replaceChildren(...data.orders.map(order => { const row = document.createElement('tr'); for (const [key, value] of Object.entries({ id: order.id, state: order.state, amount: String(order.amount) })) { const cell = document.createElement('td'); cell.dataset.testid = 'order-' + order.id + '-' + key; cell.textContent = value; row.append(cell); } return row; })); }).catch(() => { document.querySelector('[data-testid="dashboard-revenue"]').textContent = 'Unavailable'; });</script>`);

function send(response, status, body, headers = {}) {
  response.writeHead(status, { 'content-type': 'text/html; charset=utf-8', 'cache-control': 'no-store', ...headers });
  response.end(body);
}

function redirect(response, path, cookie) {
  send(response, 303, '', { location: path, ...(cookie ? { 'set-cookie': cookie } : {}) });
}

async function formData(request) {
  let body = '';
  for await (const chunk of request) {
    body += chunk;
    if (body.length > 4096) throw new Error('form too large');
  }
  return new URLSearchParams(body);
}

function session(request) {
  const match = /(?:^|;\s*)sample_session=([^;]+)/.exec(request.headers.cookie ?? '');
  return match?.[1];
}

export function createSampleServer({ defect = process.env.QA_SAMPLE_DEFECT === '1' } = {}) {
  return createServer(async (request, response) => {
    try {
      const path = new URL(request.url, 'http://sample.local').pathname;
      if (request.method === 'GET' && path === '/healthz') return send(response, 200, 'ok', { 'content-type': 'text/plain' });
      if (request.method === 'GET' && (path === '/' || path === '/login')) {
        if (sessions.has(session(request))) return redirect(response, '/dashboard');
        return send(response, 200, loginPage);
      }
      if (request.method === 'POST' && path === '/login') {
        const form = await formData(request);
        if (form.get('email') !== expectedEmail || form.get('password') !== expectedPassword) {
          return send(response, 401, loginPage.replace('</form>', '</form><p class="error" data-testid="login-error">Invalid test credentials</p>'));
        }
        const token = randomUUID();
        sessions.add(token);
        return redirect(response, '/dashboard', `sample_session=${token}; HttpOnly; SameSite=Lax; Path=/`);
      }
      if (request.method === 'POST' && path === '/signout') {
        sessions.delete(session(request));
        return redirect(response, '/login', 'sample_session=; Max-Age=0; HttpOnly; SameSite=Lax; Path=/');
      }
      if (path === '/dashboard' || path === '/api/dashboard') {
        if (!sessions.has(session(request))) return redirect(response, '/login');
        if (request.method !== 'GET') return send(response, 405, 'Method not allowed');
        if (path === '/dashboard') return send(response, 200, dashboardPage);
        const netRevenue = defect ? 230000 : orders.filter(o => o.state === 'paid').reduce((sum, o) => sum + o.amount, 0) - orders.filter(o => o.state === 'refunded').reduce((sum, o) => sum + o.amount, 0);
        return send(response, 200, JSON.stringify({ net_revenue: netRevenue, orders }), { 'content-type': 'application/json' });
      }
      send(response, 404, 'Not found');
    } catch {
      send(response, 400, 'Bad request');
    }
  });
}

if (process.argv[1] && new URL(`file://${process.argv[1]}`).href === import.meta.url) {
  const port = Number(process.env.PORT ?? 4174);
  createSampleServer().listen(port, '127.0.0.1', () => console.log(`Synthetic sample listening on http://127.0.0.1:${port}`));
}
