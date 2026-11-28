'use strict';

const translations = {
  en: {
    community: 'Community demo', language: 'Language', navigation: 'Demo pages', skip: 'Skip to content',
    channels: 'Channels', orders: 'Merchant orders', checkout: 'Checkout',
    channelsLead: 'Explore payment methods in the sandbox and see which markets require a provider integration.',
    sandboxTitle: 'Sandbox payment methods', sandboxBadge: 'Sandbox',
    tryCheckout: 'Try checkout', previewTitle: 'Markets requiring integration', previewBadge: 'Integration required',
    previewLead: 'Live collections and payouts require a separate technical integration with your upstream provider.',
    brazil: 'Brazil', us: 'United States', india: 'India', thailand: 'Thailand', indonesia: 'Indonesia',
    philippines: 'Philippines', mexico: 'Mexico', pix: 'Pix QR', card: 'Card checkout', gateway: 'Payment gateway',
    ordersLead: 'Browse sample orders. A simulated payment updates its status in this view.',
    reference: 'Merchant reference', market: 'Market', method: 'Payment method', amount: 'Amount', status: 'Status',
    paid: 'Paid in sandbox', pending: 'Awaiting simulation',
    checkoutLead: 'Choose a payment method and run a sample checkout. The simulation runs in this browser.',
    testAmount: 'Sample amount', qrNotice: 'Example Pix QR. This code cannot be used to make a payment.',
    cardNotice: 'Simulated card checkout. No card details are needed.',
    gatewayNotice: 'Simulated payment gateway. The payment result appears here.',
    simulate: 'Simulate payment', success: 'Simulation complete. The sample order is marked paid.',
    viewOrders: 'View merchant orders', footer: 'FlowXPay Community · Sample data only', contact: 'Contact us on Telegram',
  },
  zh: {
    community: '社区版演示', language: '语言', navigation: '演示页面', skip: '跳到正文',
    channels: '上游通道', orders: '商户订单', checkout: '演示收银台',
    channelsLead: '体验沙盒支付方式，查看需要接入上游服务商的市场。',
    sandboxTitle: '沙盒支付方式', sandboxBadge: '沙盒',
    tryCheckout: '体验收银台', previewTitle: '待接入市场', previewBadge: '需技术接入',
    previewLead: '开通正式代收或代付，需要单独对接对应的上游通道。',
    brazil: '巴西', us: '美国', india: '印度', thailand: '泰国', indonesia: '印度尼西亚',
    philippines: '菲律宾', mexico: '墨西哥', pix: 'Pix 二维码', card: '银行卡收银台', gateway: '支付网关',
    ordersLead: '查看示例订单。完成模拟支付后，可以在这里查看对应订单的状态。',
    reference: '商户订单号', market: '市场', method: '支付方式', amount: '金额', status: '状态',
    paid: '模拟支付成功', pending: '待模拟支付',
    checkoutLead: '选择支付方式并体验示例收银台。模拟过程在当前浏览器中完成。',
    testAmount: '示例金额', qrNotice: '这是 Pix 示例二维码，无法用于付款。',
    cardNotice: '这是模拟银行卡收银台，无需填写银行卡资料。',
    gatewayNotice: '这是模拟支付网关，模拟结果会在此页显示。',
    simulate: '模拟支付', success: '模拟支付完成，示例订单已更新为支付成功。',
    viewOrders: '查看商户订单', footer: 'FlowXPay 社区版 · 仅使用示例数据', contact: '通过 Telegram 联系商务',
  },
};

const sandboxMethods = [
  { id: 'pix', market: 'brazil', method: 'pix', currency: 'BRL', amount: 123.45, reference: 'DEMO-PIX-001', notice: 'qrNotice' },
  { id: 'card', market: 'us', method: 'card', currency: 'USD', amount: 42, reference: 'DEMO-USD-001', notice: 'cardNotice' },
  { id: 'gateway', market: 'india', method: 'gateway', currency: 'INR', amount: 2499, reference: 'DEMO-INR-001', notice: 'gatewayNotice' },
];
const previewMarkets = [
  { market: 'thailand', currency: 'THB' },
  { market: 'indonesia', currency: 'IDR' },
  { market: 'philippines', currency: 'PHP' },
  { market: 'mexico', currency: 'MXN' },
];
const views = new Set(['channels', 'orders', 'checkout']);
const paidOrders = new Set();
const main = document.querySelector('#main');
const languageSelect = document.querySelector('#language');
const announcement = document.querySelector('#announcement');
const requestedLanguage = new URLSearchParams(location.search).get('lang');
let language = requestedLanguage === 'en' || requestedLanguage === 'zh'
  ? requestedLanguage : navigator.language.toLowerCase().startsWith('zh') ? 'zh' : 'en';

function copy(key) {
  return translations[language][key];
}

function formatMoney({ amount, currency }) {
  return new Intl.NumberFormat(language === 'zh' ? 'zh-CN' : 'en-US', {
    style: 'currency', currency, currencyDisplay: 'code',
  }).format(amount);
}

function badge(key, tone) {
  return `<span class="badge ${tone}">${copy(key)}</span>`;
}

function orderStatus(id) {
  return paidOrders.has(id) ? badge('paid', 'green') : badge('pending', 'neutral');
}

function intro(title, lead) {
  return `<div class="page-heading"><h1>${copy(title)}</h1><p>${copy(lead)}</p></div>`;
}

function channelsPage() {
  return `${intro('channels', 'channelsLead')}
    <section class="section" aria-labelledby="sandbox-heading">
      <h2 id="sandbox-heading">${copy('sandboxTitle')}</h2>
      <ul class="card-grid sandbox-grid">
        ${sandboxMethods.map(item => `<li class="card sandbox-card">
          <div class="card-heading"><h3>${copy(item.market)}</h3>${badge('sandboxBadge', 'green')}</div>
          <p class="card-subtitle">${item.currency} · ${copy(item.method)}</p>
          <a class="button secondary" href="#checkout/${item.id}">${copy('tryCheckout')}</a>
        </li>`).join('')}
      </ul>
    </section>
    <section class="section" aria-labelledby="preview-heading">
      <div class="section-intro"><h2 id="preview-heading">${copy('previewTitle')}</h2><p>${copy('previewLead')}</p></div>
      <ul class="card-grid preview-grid">
        ${previewMarkets.map(item => `<li class="card preview-card">
          <h3>${copy(item.market)}</h3>
          <p class="card-subtitle">${item.currency}</p>
          ${badge('previewBadge', 'neutral')}
        </li>`).join('')}
      </ul>
    </section>`;
}

function ordersPage() {
  return `${intro('orders', 'ordersLead')}
    <ul class="order-list">
      ${sandboxMethods.map(item => `<li class="card order">
        <div><p class="field-label">${copy('reference')}</p><h2 class="reference">${item.reference}</h2></div>
        <dl class="order-details">
          <div><dt>${copy('market')}</dt><dd>${copy(item.market)}</dd></div>
          <div><dt>${copy('method')}</dt><dd>${copy(item.method)}</dd></div>
          <div><dt>${copy('amount')}</dt><dd class="value">${formatMoney(item)}</dd></div>
        </dl>
        <div class="order-action">${orderStatus(item.id)}
          <a class="button secondary" href="#checkout/${item.id}">${copy('tryCheckout')}</a>
        </div>
      </li>`).join('')}
    </ul>`;
}

function checkoutPage(item) {
  const paid = paidOrders.has(item.id);
  return `${intro('checkout', 'checkoutLead')}
    <section class="card checkout" aria-label="${copy('checkout')}">
      <label for="payment-method">${copy('method')}</label>
      <select id="payment-method">
        ${sandboxMethods.map(option => `<option value="${option.id}"${item.id === option.id ? ' selected' : ''}>${copy(option.market)} · ${copy(option.method)}</option>`).join('')}
      </select>
      <div class="checkout-summary">
        <div><p class="field-label">${copy('testAmount')}</p><p class="checkout-amount">${formatMoney(item)}</p></div>
        ${orderStatus(item.id)}
      </div>
      <div class="payment-preview">
        ${item.id === 'pix' ? '<img src="./pix-example.png" width="160" height="160" alt="">' : ''}
        <p>${copy(item.notice)}</p>
      </div>
      ${paid
        ? `<p class="success">${copy('success')}</p><a id="payment-result" class="button primary" href="#orders">${copy('viewOrders')}</a>`
        : `<button class="button primary" id="simulate" type="button">${copy('simulate')}</button>`}
    </section>`;
}

function render(focusMain = false) {
  const [route, method] = location.hash.slice(1).split('/');
  const view = views.has(route) ? route : 'channels';
  const item = sandboxMethods.find(option => option.id === method) ?? sandboxMethods[0];
  document.documentElement.lang = language === 'zh' ? 'zh-CN' : 'en';
  document.title = `${copy(view)} · FlowXPay`;
  languageSelect.value = language;
  document.querySelector('#navigation').setAttribute('aria-label', copy('navigation'));
  document.querySelector('.skip').textContent = copy('skip');
  document.querySelectorAll('[data-copy]').forEach(node => { node.textContent = copy(node.dataset.copy); });
  document.querySelectorAll('[data-view]').forEach(link => {
    if (link.dataset.view === view) link.setAttribute('aria-current', 'page');
    else link.removeAttribute('aria-current');
  });
  main.innerHTML = view === 'orders' ? ordersPage() : view === 'checkout' ? checkoutPage(item) : channelsPage();

  document.querySelector('#payment-method')?.addEventListener('change', event => {
    const selected = sandboxMethods.find(option => option.id === event.target.value);
    if (!selected) return;
    history.replaceState(null, '', `#checkout/${selected.id}`);
    render();
    document.querySelector('#payment-method').focus();
  });
  document.querySelector('#simulate')?.addEventListener('click', () => {
    paidOrders.add(item.id);
    render();
    announcement.textContent = copy('success');
    document.querySelector('#payment-result').focus();
  });
  if (focusMain) main.focus();
}

languageSelect.addEventListener('change', event => {
  language = event.target.value;
  const url = new URL(location.href);
  url.searchParams.set('lang', language);
  history.replaceState(null, '', url);
  render();
});
document.querySelector('.skip').addEventListener('click', event => {
  event.preventDefault();
  main.focus();
});
window.addEventListener('hashchange', () => {
  announcement.textContent = '';
  render(true);
});
render();
