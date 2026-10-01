/* Pocketful browser app. Plain JavaScript, no dependencies, no network
   access other than this service's own API. */
(() => {
  'use strict';

  const TOKEN_KEY = 'pocketful.token';
  const REQUEST_TIMEOUT_MS = 15000;
  const PAGE_SIZE = 200;
  const MAX_PAGES = 5;

  const app = document.getElementById('app');
  const navSlot = document.getElementById('nav');
  const userSlot = document.getElementById('user');

  let me = null; // latest GET /me body

  /* ---------- DOM helpers ---------- */

  function append(el, kids) {
    for (const kid of kids.flat(Infinity)) {
      if (kid === null || kid === undefined || kid === false) continue;
      el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
    }
  }

  function h(tag, props, ...kids) {
    const el = document.createElement(tag);
    for (const [key, value] of Object.entries(props || {})) {
      if (value === undefined || value === null || value === false) continue;
      if (key === 'class') el.className = value;
      else if (key.startsWith('on') && typeof value === 'function') el.addEventListener(key.slice(2), value);
      else if (key === 'value' || key === 'checked' || key === 'disabled') el[key] = value;
      else el.setAttribute(key, value === true ? '' : value);
    }
    append(el, kids);
    return el;
  }

  const ICONS = {
    up: 'M7 17L17 7M9 7h8v8',
    down: 'M17 7L7 17M15 17H7V9',
    swap: 'M7 7h11l-3-3M17 17H6l3 3',
    lock: 'M6 11h12v9H6zM8 11V8a4 4 0 0 1 8 0v3',
    globe: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM3 12h18M12 3c3 3 3 15 0 18M12 3c-3 3-3 15 0 18',
    clock: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM12 7v5l3 2',
    inbox: 'M4 13l2-8h12l2 8v6H4zM4 13h5l1 2h4l1-2h5',
  };

  function icon(name) {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('viewBox', '0 0 24 24');
    svg.setAttribute('aria-hidden', 'true');
    const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
    path.setAttribute('d', ICONS[name]);
    svg.append(path);
    return svg;
  }

  function uuid() {
    const b = new Uint8Array(16);
    crypto.getRandomValues(b);
    b[6] = (b[6] & 0x0f) | 0x40;
    b[8] = (b[8] & 0x3f) | 0x80;
    const hex = Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
  }

  /* ---------- Money and time ---------- */

  // minor units -> "100.00 EUR" / "1200 JPY" (exact, no floats).
  function formatAmount(minor) {
    const units = me.minor_units;
    const digits = String(Math.abs(minor)).padStart(units + 1, '0');
    const whole = digits.slice(0, digits.length - units);
    const frac = digits.slice(digits.length - units);
    return `${units ? `${whole}.${frac}` : whole} ${me.currency}`;
  }

  // minor units -> plain decimal text for an input ("20.00").
  function decimalText(minor) {
    return formatAmount(minor).replace(` ${me.currency}`, '');
  }

  class FormError extends Error {}

  // decimal text -> minor units, exactly; throws FormError on bad input.
  function parseDecimal(text) {
    const units = me.minor_units;
    const match = /^(\d+)(?:\.(\d+))?$/.exec(text.trim());
    if (!match) throw new FormError(`Enter an amount as a number, like ${units ? '15.00' : '15'}.`);
    const frac = match[2] || '';
    if (frac.length > units) {
      throw new FormError(units
        ? `${me.currency} amounts can have at most ${units} decimal places.`
        : `${me.currency} amounts are whole numbers with no decimals.`);
    }
    const minor = BigInt(match[1] + frac.padEnd(units, '0'));
    if (minor < 1n) throw new FormError('Enter an amount greater than zero.');
    if (minor > BigInt(Number.MAX_SAFE_INTEGER)) throw new FormError('That amount is too large.');
    return Number(minor);
  }

  // The equal-split rule: whole minor units, first people get the extra unit.
  function equalShares(amount, count) {
    const base = Math.floor(amount / count);
    const extra = amount % count;
    return Array.from({ length: count }, (_, i) => base + (i < extra ? 1 : 0));
  }

  function humanTime(iso) {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return iso;
    return d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
  }

  function cleanHandle(text) {
    return text.trim().replace(/^@+/, '').toLowerCase();
  }

  /* ---------- API ---------- */

  class ApiError extends Error {
    constructor(status, code, message) {
      super(message);
      this.status = status;
      this.code = code;
    }
  }

  // The response never arrived (network error, abort, timeout): the
  // outcome of the request is unknown.
  class LostResponse extends Error {}

  async function api(method, path, { body, key } = {}) {
    const headers = { Accept: 'application/json' };
    const token = localStorage.getItem(TOKEN_KEY);
    if (token) headers.Authorization = `Bearer ${token}`;
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    if (key) headers['Idempotency-Key'] = key;
    const ctl = new AbortController();
    const timer = setTimeout(() => ctl.abort(), REQUEST_TIMEOUT_MS);
    let res;
    let text;
    try {
      res = await fetch(path, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: ctl.signal,
      });
      text = await res.text();
    } catch (err) {
      throw new LostResponse(String(err));
    } finally {
      clearTimeout(timer);
    }
    let data = null;
    if (text) {
      try { data = JSON.parse(text); } catch (err) { data = null; }
    }
    if (res.ok) return data;
    const e = (data && data.error) || {};
    if (res.status === 401 && !path.startsWith('/auth/')) signedOut();
    throw new ApiError(res.status, e.code || 'error', e.message || 'Something went wrong.');
  }

  async function fetchAll(path, field) {
    const items = [];
    for (let page = 0; page < MAX_PAGES; page++) {
      const data = await api('GET', `${path}${path.includes('?') ? '&' : '?'}limit=${PAGE_SIZE}&offset=${page * PAGE_SIZE}`);
      items.push(...data[field]);
      if (!data.has_more) break;
    }
    return items;
  }

  const MESSAGES = {
    insufficient_funds: 'Your available balance is too low for that.',
    self_payment: 'You can’t send money to yourself.',
    self_request: 'You can’t request money from yourself.',
    request_not_pending: 'That request is no longer pending.',
    authorization_not_open: 'That authorization is no longer open.',
    authorization_expired: 'That authorization has expired.',
    capture_exceeds_authorization: 'That is more than is still reserved.',
    forbidden: 'You can’t do that with this item.',
  };

  function explain(err, notFound) {
    if (err.code === 'not_found') return notFound || 'We couldn’t find that.';
    return MESSAGES[err.code] || err.message;
  }

  /* ---------- Session and page chrome ---------- */

  function signedOut() {
    localStorage.removeItem(TOKEN_KEY);
    location.replace('/login');
  }

  function renderChrome() {
    navSlot.replaceChildren();
    userSlot.replaceChildren();
    const path = currentPath();
    const links = me
      ? [['/', 'Wallet'], ['/requests', 'Requests'], ['/split', 'Split'], ['/authorizations', 'Holds']]
      : [['/login', 'Log in'], ['/signup', 'Sign up']];
    for (const [href, label] of links) {
      navSlot.append(h('a', { href, 'aria-current': href === path ? 'page' : null }, label));
    }
    if (!me) return;
    userSlot.append(
      h('span', { class: 'avatar', 'aria-hidden': 'true' }, (me.display_name || '?').charAt(0).toUpperCase()),
      h('span', { class: 'user-name', 'data-testid': 'current-user' }, me.display_name),
      h('span', { class: 'user-handle' }, '@', h('span', { 'data-testid': 'current-handle' }, me.handle)),
      h('button', { class: 'btn small', type: 'button', 'data-testid': 'logout-button', onclick: signedOut }, 'Log out'),
    );
  }

  function currentPath() {
    const p = location.pathname.replace(/\/+$/, '');
    return p === '' ? '/' : p;
  }

  /* ---------- Alerts and the write flow ---------- */

  // A set of mutually exclusive messages, each present only while shown.
  function alertBox(prefix) {
    const box = h('div', { class: 'alerts' });
    const kinds = { error: 'alert-error', uncertain: 'alert-uncertain', success: 'alert-success' };
    return {
      el: box,
      show(kind, text) {
        box.replaceChildren(h('div', {
          class: `alert ${kinds[kind]}`,
          role: kind === 'error' ? 'alert' : 'status',
          'data-testid': `${prefix}-${kind}`,
        }, text));
      },
      clear() { box.replaceChildren(); },
    };
  }

  // Runs one idempotent create form. An unchanged resubmit after success
  // sends nothing; after a lost response it retries with the same key and
  // body; any other change is a new operation with a new key.
  function writeFlow({ path, alerts, read, done, refused, success, uncertain, notFound }) {
    let last = null;
    let inflight = false;

    return async function submit(event, button) {
      event.preventDefault();
      if (inflight) return;
      let body;
      try {
        body = read();
      } catch (err) {
        if (!(err instanceof FormError)) throw err;
        alerts.show('error', err.message);
        return;
      }
      const sig = JSON.stringify(body);
      if (last && last.sig === sig) {
        if (last.state === 'done') return;
      } else {
        last = { sig, key: uuid(), state: 'new' };
      }
      inflight = true;
      button.disabled = true;
      button.setAttribute('aria-busy', 'true');
      try {
        const data = await api('POST', path, { body, key: last.key });
        last.state = 'done';
        alerts.show('success', success(data, body));
        await done(data);
      } catch (err) {
        if (err instanceof LostResponse || (err instanceof ApiError && err.status >= 500)) {
          last.state = 'uncertain';
          alerts.show('uncertain', uncertain);
        } else if (err instanceof ApiError) {
          last = null;
          alerts.show('error', explain(err, notFound));
          await refused();
        } else {
          throw err;
        }
      } finally {
        inflight = false;
        button.disabled = false;
        button.removeAttribute('aria-busy');
      }
    };
  }

  function field(id, label, control, hint) {
    return h('div', { class: 'field' },
      h('label', { for: id }, label),
      control,
      hint ? h('p', { class: 'hint' }, hint) : null);
  }

  function textInput(testid, props) {
    return h('input', { id: testid, 'data-testid': testid, type: 'text', autocomplete: 'off', ...props });
  }

  function handleInput(testid) {
    return h('div', { class: 'input-prefix' }, h('span', { 'aria-hidden': 'true' }, '@'),
      textInput(testid, { autocapitalize: 'none', spellcheck: 'false', placeholder: 'handle' }));
  }

  function visibilitySelect(testid) {
    return h('select', { id: testid, 'data-testid': testid },
      h('option', { value: 'public' }, 'Public – shown in the activity feed'),
      h('option', { value: 'private' }, 'Private – only you and the recipient'));
  }

  function amountHint() {
    return `Amount in ${me.currency}, for example ${me.minor_units ? '15.00' : '15'}`;
  }

  /* ---------- Wallet ---------- */

  function walletCard({ refresh }) {
    const body = h('div', null, h('span', { class: 'skeleton', 'aria-label': 'Loading balance' }));
    const card = h('section', { class: 'card wallet', 'aria-label': 'Wallet' }, body);
    let refreshButton = null;
    if (refresh) {
      refreshButton = h('button', { class: 'btn small', type: 'button', 'data-testid': 'wallet-refresh', onclick: refresh }, 'Refresh');
      card.append(h('div', { class: 'wallet-actions' }, refreshButton));
    }
    return {
      el: card,
      busy(on) {
        if (refreshButton) refreshButton.textContent = on ? 'Refreshing…' : 'Refresh';
      },
      update(m) {
        const held = m.held > 0;
        body.replaceChildren(
          h('p', { class: 'eyebrow' }, 'Available to spend'),
          h('p', { class: 'wallet-available', 'data-testid': 'wallet-available', 'data-amount': m.available }, formatAmount(m.available)),
          h('dl', { class: 'wallet-secondary' },
            h('div', null, h('dt', null, 'Total balance'),
              h('dd', { 'data-testid': 'wallet-balance', 'data-amount': m.total }, formatAmount(m.total))),
            held ? h('div', { class: 'held' }, h('dt', null, 'On hold'),
              h('dd', { 'data-testid': 'wallet-held', 'data-amount': m.held }, formatAmount(m.held))) : null),
        );
      },
    };
  }

  /* ---------- Home ---------- */

  function feedItem(p) {
    const mine = p.from_user_id === me.user_id;
    const theirs = p.to_user_id === me.user_id;
    const role = mine ? 'out' : theirs ? 'in' : '';
    const label = mine ? 'Sent' : theirs ? 'Received' : 'Public payment';
    return h('li', {
      class: 'row', 'data-testid': `activity-item-${p.payment_id}`, 'data-visibility': p.visibility,
    },
      h('span', { class: `dir-icon ${role}` }, icon(mine ? 'up' : theirs ? 'down' : 'swap')),
      h('div', { class: 'row-main' },
        h('p', { class: 'row-title' }, h('span', { class: 'visually-hidden' }, `${label}: `),
          h('span', { 'data-testid': `activity-parties-${p.payment_id}` }, `@${p.from_handle} paid @${p.to_handle}`)),
        h('p', { class: 'row-note', 'data-testid': `activity-note-${p.payment_id}` }, p.note),
        h('div', { class: 'row-meta' },
          h('span', { class: `badge ${p.visibility}` }, icon(p.visibility === 'private' ? 'lock' : 'globe'),
            p.visibility === 'private' ? 'Private' : 'Public'),
          h('time', { datetime: p.created_at }, humanTime(p.created_at)))),
      h('div', { class: `row-amount ${role}`, 'data-testid': `activity-amount-${p.payment_id}` }, formatAmount(p.amount)));
  }

  function emptyState(testid, title, text) {
    return h('div', { class: 'card empty', 'data-testid': testid }, icon('inbox'), h('strong', null, title), text);
  }

  function moneyForms(refreshHome) {
    const pay = payForm(refreshHome);
    const request = requestForm(refreshHome);
    return { pay, request };
  }

  function payForm(afterWrite) {
    const handle = handleInput('pay-handle');
    const amount = textInput('pay-amount', { inputmode: 'decimal', placeholder: me.minor_units ? '0.00' : '0' });
    const note = textInput('pay-note', { maxlength: '400' });
    const visibility = visibilitySelect('pay-visibility');
    const alerts = alertBox('pay');
    const button = h('button', { class: 'btn primary', type: 'submit', 'data-testid': 'pay-submit' }, 'Send money');
    const submit = writeFlow({
      path: '/payments',
      alerts,
      read: () => ({
        to_handle: cleanHandle(handle.querySelector('input').value),
        amount: parseDecimal(amount.value),
        note: note.value,
        visibility: visibility.value,
      }),
      success: (data) => `Sent ${formatAmount(data.amount)} to @${data.to_handle}.`,
      uncertain: 'We didn’t get a reply, so we can’t tell whether this payment went through. '
        + 'Nothing will be sent twice – press “Send money” again to check and finish it.',
      notFound: 'No one has that handle.',
      done: afterWrite,
      refused: afterWrite,
    });
    return h('form', { class: 'card', novalidate: true, onsubmit: (e) => submit(e, button) },
      h('h2', null, 'Send money'),
      h('p', { class: 'card-lead' }, 'Pay anyone by their handle, instantly.'),
      field('pay-handle', 'Recipient', handle),
      field('pay-amount', 'Amount', amount, amountHint()),
      field('pay-note', h('span', null, 'Note ', h('span', { class: 'optional' }, '(optional)')), note),
      field('pay-visibility', 'Who can see it', visibility),
      alerts.el,
      button);
  }

  function requestForm(afterWrite) {
    const handle = handleInput('request-handle');
    const amount = textInput('request-amount', { inputmode: 'decimal', placeholder: me.minor_units ? '0.00' : '0' });
    const note = textInput('request-note', { maxlength: '400' });
    const alerts = alertBox('request');
    const button = h('button', { class: 'btn primary', type: 'submit', 'data-testid': 'request-submit' }, 'Request money');
    const submit = writeFlow({
      path: '/requests',
      alerts,
      read: () => ({
        payer_handle: cleanHandle(handle.querySelector('input').value),
        amount: parseDecimal(amount.value),
        note: note.value,
      }),
      success: (data) => `Requested ${formatAmount(data.amount)} from @${data.payer_handle}.`,
      uncertain: 'We didn’t get a reply, so we can’t tell whether the request was created. Press “Request money” again to check.',
      notFound: 'No one has that handle.',
      done: afterWrite,
      refused: async () => {},
    });
    return h('form', { class: 'card', novalidate: true, onsubmit: (e) => submit(e, button) },
      h('h2', null, 'Request money'),
      h('p', { class: 'card-lead' }, 'Ask someone to pay you. They decide when, and how visibly.'),
      field('request-handle', 'Ask', handle),
      field('request-amount', 'Amount', amount, amountHint()),
      field('request-note', h('span', null, 'Note ', h('span', { class: 'optional' }, '(optional)')), note),
      alerts.el,
      button);
  }

  function authorizeForm(afterWrite) {
    const handle = handleInput('authorize-handle');
    const amount = textInput('authorize-amount', { inputmode: 'decimal', placeholder: me.minor_units ? '0.00' : '0' });
    const note = textInput('authorize-note', { maxlength: '400' });
    const visibility = visibilitySelect('authorize-visibility');
    const alerts = alertBox('authorize');
    const button = h('button', { class: 'btn primary', type: 'submit', 'data-testid': 'authorize-submit' }, 'Reserve funds');
    const submit = writeFlow({
      path: '/authorizations',
      alerts,
      read: () => ({
        to_handle: cleanHandle(handle.querySelector('input').value),
        amount: parseDecimal(amount.value),
        note: note.value,
        visibility: visibility.value,
      }),
      success: (data) => `Reserved ${formatAmount(data.amount)} for @${data.to_handle}.`,
      uncertain: 'We didn’t get a reply, so we can’t tell whether the funds were reserved. Press “Reserve funds” again to check.',
      notFound: 'No one has that handle.',
      done: afterWrite,
      refused: afterWrite,
    });
    return h('form', { class: 'card', novalidate: true, onsubmit: (e) => submit(e, button) },
      h('h2', null, 'Reserve funds'),
      h('p', { class: 'card-lead' }, 'Hold money for someone to collect later. Held funds can’t be spent until they’re collected, released or expired.'),
      field('authorize-handle', 'Recipient', handle),
      field('authorize-amount', 'Amount to hold', amount, amountHint()),
      field('authorize-note', h('span', null, 'Note ', h('span', { class: 'optional' }, '(optional)')), note),
      field('authorize-visibility', 'Who can see the payment', visibility),
      alerts.el,
      button);
  }

  async function homePage() {
    const wallet = walletCard({ refresh: () => refreshHome() });
    const feedSlot = h('div', { 'aria-live': 'polite' }, h('p', { class: 'loading-note' }, 'Loading activity…'));
    let seq = 0;

    // Latest refresh wins: a slower, earlier read is dropped.
    async function refreshHome() {
      const mine = ++seq;
      wallet.busy(true);
      try {
        const [m, payments] = await Promise.all([api('GET', '/me'), fetchAll('/activity', 'payments')]);
        if (mine !== seq) return;
        me = m;
        wallet.update(m);
        renderFeed(payments);
      } catch (err) {
        if (mine !== seq || !(err instanceof LostResponse || err instanceof ApiError)) return;
        feedSlot.replaceChildren(h('div', { class: 'alert alert-error', role: 'alert' },
          'We couldn’t load your activity. ',
          h('button', { class: 'btn small', type: 'button', onclick: () => refreshHome() }, 'Try again')));
      } finally {
        if (mine === seq) wallet.busy(false);
      }
    }

    function renderFeed(payments) {
      if (payments.length === 0) {
        feedSlot.replaceChildren(emptyState('empty-activity', 'No activity yet',
          'Payments you send or receive, and public payments, will appear here.'));
        return;
      }
      feedSlot.replaceChildren(h('ul', { class: 'rows', 'data-testid': 'activity-list' }, payments.map(feedItem)));
    }

    wallet.update(me);
    const { pay, request } = moneyForms(refreshHome);
    app.replaceChildren(
      h('h1', { class: 'page-title' }, 'Your wallet'),
      wallet.el,
      h('div', { class: 'grid two' }, pay, request),
      h('div', { class: 'grid' }, authorizeForm(refreshHome)),
      h('h2', { class: 'section-title' }, 'Recent activity'),
      feedSlot);
    await refreshHome();
  }

  /* ---------- Requests ---------- */

  const STATUS_LABEL = { pending: 'Pending', paid: 'Paid', declined: 'Declined', cancelled: 'Cancelled' };

  async function requestsPage() {
    const errorSlot = h('div', null);
    const lists = h('div', { 'aria-live': 'polite' }, h('p', { class: 'loading-note' }, 'Loading requests…'));
    const attempts = new Map(); // request id -> { key } kept so a lost pay can be retried with the same key
    let seq = 0;

    function showError(text) {
      errorSlot.replaceChildren(...(text
        ? [h('div', { class: 'alert alert-error', role: 'alert', 'data-testid': 'request-error' }, text)]
        : []));
    }

    async function refresh() {
      const mine = ++seq;
      try {
        const requests = await fetchAll('/requests', 'requests');
        if (mine !== seq) return;
        render(requests);
      } catch (err) {
        if (mine !== seq || !(err instanceof LostResponse || err instanceof ApiError)) return;
        lists.replaceChildren(h('div', { class: 'alert alert-error', role: 'alert' },
          'We couldn’t load your requests. ',
          h('button', { class: 'btn small', type: 'button', onclick: refresh }, 'Try again')));
      }
    }

    async function act(request, verb, button, body) {
      showError(null);
      button.disabled = true;
      try {
        if (verb === 'pay') {
          let attempt = attempts.get(request.request_id);
          if (!attempt || attempt.sig !== JSON.stringify(body)) {
            attempt = { key: uuid(), sig: JSON.stringify(body) };
            attempts.set(request.request_id, attempt);
          }
          await api('POST', `/requests/${request.request_id}/pay`, { body, key: attempt.key });
          attempts.delete(request.request_id);
        } else {
          await api('POST', `/requests/${request.request_id}/${verb}`);
        }
      } catch (err) {
        if (err instanceof LostResponse) {
          showError('We didn’t get a reply, so we can’t tell whether that went through. Press the button again to check.');
        } else if (err instanceof ApiError) {
          attempts.delete(request.request_id);
          showError(explain(err, 'That request no longer exists.'));
        } else {
          throw err;
        }
      }
      await refresh();
    }

    function requestItem(r) {
      const incoming = r.payer_id === me.user_id;
      const other = incoming ? r.requester_handle : r.payer_handle;
      const pending = r.status === 'pending';
      const actions = [];
      if (pending && incoming) {
        const visibility = h('select', { id: `request-visibility-${r.request_id}`, 'aria-label': 'Who can see the payment', 'data-testid': `request-visibility-${r.request_id}` },
          h('option', { value: 'public' }, 'Public payment'), h('option', { value: 'private' }, 'Private payment'));
        const pay = h('button', { class: 'btn primary small', type: 'button', 'data-testid': `request-pay-${r.request_id}` }, 'Pay');
        pay.addEventListener('click', () => act(r, 'pay', pay, { visibility: visibility.value }));
        const decline = h('button', { class: 'btn small', type: 'button', 'data-testid': `request-decline-${r.request_id}` }, 'Decline');
        decline.addEventListener('click', () => act(r, 'decline', decline));
        actions.push(pay, visibility, decline);
      }
      if (pending && !incoming) {
        const cancel = h('button', { class: 'btn small danger', type: 'button', 'data-testid': `request-cancel-${r.request_id}` }, 'Cancel request');
        cancel.addEventListener('click', () => act(r, 'cancel', cancel));
        actions.push(cancel);
      }
      return h('li', { class: 'row', 'data-testid': `request-item-${r.request_id}`, 'data-status': r.status },
        h('span', { class: `dir-icon ${incoming ? 'out' : 'in'}` }, icon(incoming ? 'up' : 'down')),
        h('div', { class: 'row-main' },
          h('p', { class: 'row-title' }, incoming ? `@${other} is asking you to pay` : `You asked @${other}`),
          h('p', { class: 'row-note' }, r.note),
          h('div', { class: 'row-meta' },
            h('span', { class: `badge ${r.status}` }, STATUS_LABEL[r.status]),
            h('time', { datetime: r.created_at }, humanTime(r.created_at))),
          actions.length ? h('div', { class: 'row-actions' }, actions) : null),
        h('div', { class: 'row-amount', 'data-testid': `request-amount-${r.request_id}` }, formatAmount(r.amount)));
    }

    function section(title, testid, items, none) {
      return h('section', null,
        h('h2', { class: 'group-title' }, title),
        items.length ? h('ul', { class: 'rows', 'data-testid': testid }, items.map(requestItem))
          : h('div', { class: 'empty-inline' }, h('ul', { 'data-testid': testid }), h('p', { class: 'preview-hint' }, none)));
    }

    function render(requests) {
      if (requests.length === 0) {
        lists.replaceChildren(
          emptyState('empty-requests', 'No requests yet', 'Requests you send or receive will show up here.'),
          h('div', { hidden: true }, h('ul', { 'data-testid': 'incoming-list' }), h('ul', { 'data-testid': 'outgoing-list' })));
        return;
      }
      const incoming = requests.filter((r) => r.payer_id === me.user_id);
      const outgoing = requests.filter((r) => r.requester_id === me.user_id);
      lists.replaceChildren(
        section('Asking you to pay', 'incoming-list', incoming, 'Nobody is asking you for money.'),
        section('You asked for', 'outgoing-list', outgoing, 'You haven’t asked anyone for money.'));
    }

    app.replaceChildren(h('h1', { class: 'page-title' }, 'Requests'), errorSlot, lists);
    await refresh();
  }

  /* ---------- Split ---------- */

  function splitPage() {
    const amount = textInput('split-amount', { inputmode: 'decimal', placeholder: me.minor_units ? '0.00' : '0' });
    const handles = textInput('split-handles', { placeholder: 'ada, bob, cy', autocapitalize: 'none', spellcheck: 'false' });
    const note = textInput('split-note', { maxlength: '400' });
    const preview = h('div', { class: 'preview', 'data-testid': 'split-preview', 'aria-live': 'polite' });
    const alerts = alertBox('split');
    const result = h('div', null);
    const button = h('button', { class: 'btn primary', type: 'submit', 'data-testid': 'split-submit' }, 'Split the bill');

    function participants() {
      return handles.value.split(',').map(cleanHandle).filter((x) => x !== '');
    }

    function updatePreview() {
      const people = participants();
      let total = null;
      try { total = parseDecimal(amount.value); } catch (err) { total = null; }
      if (total === null || people.length === 0) {
        preview.replaceChildren(h('p', { class: 'preview-hint' }, 'Enter an amount and handles to preview each share.'));
        return;
      }
      if (new Set(people).size !== people.length) {
        preview.replaceChildren(h('p', { class: 'preview-hint' }, 'Each person can only be listed once.'));
        return;
      }
      const shares = equalShares(total, people.length);
      preview.replaceChildren(
        h('ul', null, people.map((who, i) => h('li', null,
          h('span', { class: 'who' }, `@${who}`, who === me.handle ? h('span', { class: 'you' }, ' (you – not requested)') : null),
          h('span', { class: 'share', 'data-testid': `split-share-${who}` }, formatAmount(shares[i]))))),
        h('p', { class: 'preview-hint total-line' }, `Total ${formatAmount(total)}, shared as evenly as possible.`));
    }

    const submit = writeFlow({
      path: '/splits',
      alerts,
      read: () => {
        const people = participants();
        if (people.length === 0) throw new FormError('Add at least one handle, separated by commas.');
        if (new Set(people).size !== people.length) throw new FormError('Each person can only be listed once.');
        return { amount: parseDecimal(amount.value), participant_handles: people, note: note.value };
      },
      success: (data) => `Split created. ${data.requests.length} request${data.requests.length === 1 ? '' : 's'} sent.`,
      uncertain: 'We didn’t get a reply, so we can’t tell whether the split was created. Press “Split the bill” again to check.',
      notFound: 'One of those handles doesn’t exist.',
      done: async (data) => {
        result.replaceChildren(h('p', { class: 'hint' }, h('a', { href: '/requests' }, 'View your requests'),
          data.requests.length ? '' : ' – nobody else was included, so no requests were needed.'));
      },
      refused: async () => {},
    });
    for (const el of [amount, handles]) el.addEventListener('input', updatePreview);

    app.replaceChildren(
      h('h1', { class: 'page-title' }, 'Split a bill'),
      h('p', { class: 'page-lead' }, 'Enter what you paid and who shared it. Everyone else gets a request for their share.'),
      h('form', { class: 'card', novalidate: true, onsubmit: (e) => submit(e, button) },
        field('split-amount', 'Total amount', amount, amountHint()),
        field('split-handles', 'People', handles, 'Handles separated by commas, in order. The first people get any extra unit.'),
        field('split-note', h('span', null, 'Note ', h('span', { class: 'optional' }, '(optional)')), note),
        h('p', { class: 'eyebrow' }, 'Shares'),
        preview,
        alerts.el,
        button,
        result));
    updatePreview();
  }

  /* ---------- Authorizations ---------- */

  async function authorizationsPage() {
    const wallet = walletCard({});
    const errorSlot = h('div', null);
    const listSlot = h('div', { 'aria-live': 'polite' }, h('p', { class: 'loading-note' }, 'Loading holds…'));
    const attempts = new Map(); // authorization id -> { sig, key }
    let seq = 0;

    function showError(text) {
      errorSlot.replaceChildren(...(text
        ? [h('div', { class: 'alert alert-error', role: 'alert', 'data-testid': 'authorization-error' }, text)]
        : []));
    }

    async function refresh() {
      const mine = ++seq;
      try {
        const [m, items] = await Promise.all([api('GET', '/me'), fetchAll('/authorizations', 'authorizations')]);
        if (mine !== seq) return;
        me = m;
        wallet.update(m);
        render(items);
      } catch (err) {
        if (mine !== seq || !(err instanceof LostResponse || err instanceof ApiError)) return;
        listSlot.replaceChildren(h('div', { class: 'alert alert-error', role: 'alert' },
          'We couldn’t load your holds. ',
          h('button', { class: 'btn small', type: 'button', onclick: refresh }, 'Try again')));
      }
    }

    async function capture(a, amountInput, keepInput, button) {
      showError(null);
      let amount;
      try {
        amount = parseDecimal(amountInput.value);
      } catch (err) {
        if (!(err instanceof FormError)) throw err;
        showError(err.message);
        return;
      }
      const body = { amount };
      if (keepInput.checked) body.final = false;
      const sig = JSON.stringify(body);
      let attempt = attempts.get(a.authorization_id);
      if (!attempt || attempt.sig !== sig) {
        attempt = { sig, key: uuid() };
        attempts.set(a.authorization_id, attempt);
      }
      button.disabled = true;
      try {
        await api('POST', `/authorizations/${a.authorization_id}/capture`, { body, key: attempt.key });
        attempts.delete(a.authorization_id);
      } catch (err) {
        if (err instanceof LostResponse) {
          showError('We didn’t get a reply, so we can’t tell whether the capture went through. Press “Collect” again to check.');
        } else if (err instanceof ApiError) {
          attempts.delete(a.authorization_id);
          showError(explain(err, 'That authorization no longer exists.'));
        } else {
          throw err;
        }
      }
      await refresh();
    }

    async function voidHold(a, button) {
      showError(null);
      button.disabled = true;
      try {
        await api('POST', `/authorizations/${a.authorization_id}/void`);
      } catch (err) {
        if (err instanceof LostResponse) showError('We didn’t get a reply. Press “Release” again to check.');
        else if (err instanceof ApiError) showError(explain(err, 'That authorization no longer exists.'));
        else throw err;
      }
      await refresh();
    }

    const STATUS = { open: 'Open', captured: 'Captured', voided: 'Released', expired: 'Expired' };

    function item(a) {
      const outgoing = a.from_user_id === me.user_id;
      const id = a.authorization_id;
      const open = a.status === 'open';
      const meta = [
        h('span', { class: `badge ${a.status}` }, STATUS[a.status]),
        h('span', { class: `badge ${a.visibility}` }, icon(a.visibility === 'private' ? 'lock' : 'globe'), a.visibility === 'private' ? 'Private' : 'Public'),
        h('span', null, icon('clock'), ` ${a.status === 'expired' ? 'Expired' : 'Expires'} ${humanTime(a.expires_at)} `),
        h('time', { class: 'raw-ts', datetime: a.expires_at, 'data-testid': `authorization-expires-${id}` }, a.expires_at),
      ];
      const progress = [];
      if (a.status === 'captured') {
        progress.push(h('p', { class: 'row-note' }, 'Captured ', h('strong', { 'data-testid': `authorization-captured-${id}` }, formatAmount(a.captured_amount))));
      } else if (a.captured_amount > 0) {
        progress.push(h('p', { class: 'row-note' }, `Collected ${formatAmount(a.captured_amount)} so far.`));
      }
      let actions = null;
      if (open && !outgoing) {
        const amountInput = textInput(`authorization-capture-amount-${id}`, { inputmode: 'decimal', value: decimalText(a.remaining_amount) });
        const keep = h('input', { type: 'checkbox', id: `authorization-keep-${id}`, 'data-testid': `authorization-keep-${id}` });
        const button = h('button', { class: 'btn primary small', type: 'button', 'data-testid': `authorization-capture-${id}` }, 'Collect');
        button.addEventListener('click', () => capture(a, amountInput, keep, button));
        actions = h('div', { class: 'capture-box' },
          field(`authorization-capture-amount-${id}`, 'Amount to collect', amountInput),
          button,
          h('label', { class: 'check', for: `authorization-keep-${id}` }, keep, 'Keep the rest on hold'));
      }
      if (open && outgoing) {
        const button = h('button', { class: 'btn small danger', type: 'button', 'data-testid': `authorization-void-${id}` }, 'Release hold');
        button.addEventListener('click', () => voidHold(a, button));
        actions = h('div', { class: 'row-actions' }, button);
      }
      return h('li', { class: 'row', 'data-testid': `authorization-item-${id}`, 'data-status': a.status },
        h('span', { class: `dir-icon ${outgoing ? 'out' : 'in'}` }, icon(outgoing ? 'up' : 'down')),
        h('div', { class: 'row-main' },
          h('p', { class: 'row-title' }, outgoing ? `Held for @${a.to_handle}` : `Held for you by @${a.from_handle}`),
          h('p', { class: 'row-note' }, a.note),
          progress,
          h('div', { class: 'row-meta' }, meta),
          actions),
        h('div', { class: 'row-amount', 'data-testid': `authorization-amount-${id}` }, formatAmount(a.amount)));
    }

    function render(items) {
      listSlot.replaceChildren(items.length === 0
        ? emptyState('empty-authorizations', 'No holds yet', 'Funds you reserve, or that others reserve for you, will appear here.')
        : h('ul', { class: 'rows', 'data-testid': 'authorization-list' }, items.map(item)));
    }

    wallet.update(me);
    app.replaceChildren(
      h('h1', { class: 'page-title' }, 'Holds'),
      h('p', { class: 'page-lead' }, 'Reserve money now, and let the recipient collect it later.'),
      wallet.el,
      h('div', { class: 'grid' }, authorizeForm(refresh)),
      h('h2', { class: 'section-title' }, 'Your holds'),
      errorSlot,
      listSlot);
    await refresh();
  }

  /* ---------- Signup and login ---------- */

  function authPage(kind) {
    const signup = kind === 'signup';
    const email = textInput(`${kind}-email`, { type: 'email', autocomplete: 'email', autocapitalize: 'none', spellcheck: 'false' });
    const password = textInput(`${kind}-password`, { type: 'password', autocomplete: signup ? 'new-password' : 'current-password' });
    const name = signup ? textInput('signup-display-name', { autocomplete: 'name' }) : null;
    const errorSlot = h('div', null);
    const button = h('button', { class: 'btn primary', type: 'submit', 'data-testid': `${kind}-submit` }, signup ? 'Create account' : 'Log in');
    let inflight = false;

    const AUTH_MESSAGES = {
      email_taken: 'That email is already registered. Try logging in instead.',
      handle_taken: 'The handle that email would give you is already taken. Try a different email.',
      unauthenticated: 'That email and password don’t match.',
    };

    async function submit(event) {
      event.preventDefault();
      if (inflight) return;
      inflight = true;
      button.disabled = true;
      errorSlot.replaceChildren();
      try {
        const body = signup
          ? { email: email.value.trim(), password: password.value, display_name: name.value.trim() }
          : { email: email.value.trim(), password: password.value };
        const data = await api('POST', `/auth/${kind}`, { body });
        localStorage.setItem(TOKEN_KEY, data.token);
        location.assign('/');
        return;
      } catch (err) {
        const text = err instanceof ApiError ? (AUTH_MESSAGES[err.code] || err.message)
          : 'We couldn’t reach the service. Check your connection and try again.';
        errorSlot.replaceChildren(h('div', { class: 'alert alert-error', role: 'alert', 'data-testid': 'auth-error' }, text));
      }
      inflight = false;
      button.disabled = false;
    }

    app.replaceChildren(h('div', { class: 'auth' },
      h('h1', { class: 'page-title' }, signup ? 'Create your account' : 'Welcome back'),
      h('form', { class: 'card', novalidate: true, onsubmit: submit },
        signup ? field('signup-display-name', 'Display name', name) : null,
        field(`${kind}-email`, 'Email', email),
        field(`${kind}-password`, 'Password', password, signup ? 'At least 8 characters.' : null),
        errorSlot,
        button),
      h('p', { class: 'auth-switch' },
        signup ? 'Already have an account? ' : 'New here? ',
        h('a', { href: signup ? '/login' : '/signup' }, signup ? 'Log in' : 'Create an account'))));
  }

  /* ---------- Router ---------- */

  const PROTECTED = {
    '/': homePage,
    '/requests': requestsPage,
    '/split': splitPage,
    '/authorizations': authorizationsPage,
  };

  async function start() {
    const path = currentPath();
    if (localStorage.getItem(TOKEN_KEY)) {
      try {
        me = await api('GET', '/me');
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) return;
        me = null;
      }
    }
    renderChrome();
    if (path === '/signup') return authPage('signup');
    if (path === '/login') return authPage('login');
    const page = PROTECTED[path];
    if (!page) {
      app.replaceChildren(h('h1', { class: 'page-title' }, 'Page not found'), h('a', { href: '/' }, 'Back to your wallet'));
      return undefined;
    }
    if (!me) {
      if (!localStorage.getItem(TOKEN_KEY)) {
        location.replace('/login');
        return undefined;
      }
      app.replaceChildren(h('div', { class: 'alert alert-error', role: 'alert' }, 'We couldn’t reach the service. Reload to try again.'));
      return undefined;
    }
    return page();
  }

  start();
})();
