// DataStack load generator: one iteration is one customer journey, from the
// ad click to the checkout.
//
//   k6 run deploy/k6/checkout-journey.js                       # local, against port-forwards
//   RATE=20 k6 run deploy/k6/checkout-journey.js               # 20 journeys per second
//
// In kind it runs as a Deployment (see deploy/k6/deployment.yaml and
// `make load`).
import http from 'k6/http';
import { check } from 'k6';

const RATE = Number(__ENV.RATE || 5); // journeys per second
const DURATION = __ENV.DURATION || '24h';

const urls = {
  customer: __ENV.CUSTOMER_URL || 'http://localhost:8081',
  marketing: __ENV.MARKETING_URL || 'http://localhost:8082',
  inventory: __ENV.INVENTORY_URL || 'http://localhost:8083',
  order: __ENV.ORDER_URL || 'http://localhost:8080',
};

// Behaviour mix, as probabilities per journey.
const NEW_CUSTOMER = 0.3; // otherwise a returning customer
const ORGANIC = 0.15; // arrives without an ad click
const LIFECYCLE_CHANGE = 0.01; // a tier upgrade or a suspension
const RESTOCK_BURST = 0.005; // a manual restock of a random product

// Where customers live (ISO 3166-1 alpha-2), weighted.
const COUNTRIES = weighted({ US: 40, GB: 12, DE: 10, CA: 8, FR: 7, NG: 6, IN: 6, BR: 6, AU: 5 });
// Paid channels get most clicks; organic comes from ORGANIC above.
const CAMPAIGN_WEIGHTS = { google: 35, facebook: 25, instagram: 15, tiktok: 10, email: 15 };
const QUANTITIES = weighted({ 1: 75, 2: 18, 3: 7 });

export const options = {
  scenarios: {
    journeys: {
      executor: 'constant-arrival-rate',
      rate: RATE,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: Math.max(10, RATE * 2),
      maxVUs: Math.max(50, RATE * 10),
    },
  },
  // Business failures (422: out of stock, declined card) are expected.
  // Only 5xx means the system is broken.
  thresholds: {
    'checks{check:no server error}': ['rate>0.99'],
  },
};

const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

// setup runs once and hands the catalog and campaigns to every VU.
export function setup() {
  const products = http.get(`${urls.inventory}/inventory`).json().map((p) => p.product_id);
  const campaigns = http
    .get(`${urls.marketing}/campaigns`)
    .json()
    .filter((c) => c.utm_source !== 'organic');

  if (products.length === 0 || campaigns.length === 0) {
    throw new Error('inventory catalog or marketing campaigns are empty; are the services migrated?');
  }
  return { products, campaigns };
}

// Customers this VU has created, reused as returning customers.
const knownCustomers = [];

export default function (data) {
  const userId = pickCustomer();
  if (!userId) {
    return;
  }

  if (Math.random() < LIFECYCLE_CHANGE) {
    changeLifecycle(userId);
  }
  if (Math.random() < RESTOCK_BURST) {
    restock(pick(data.products));
  }

  const attributionId = Math.random() < ORGANIC ? null : clickAd(userId, data.campaigns);
  checkout(userId, popularProduct(data.products), attributionId);
}

function pickCustomer() {
  if (knownCustomers.length > 0 && Math.random() >= NEW_CUSTOMER) {
    return pick(knownCustomers);
  }

  const email = `k6-${__VU}-${__ITER}-${Date.now()}@example.com`;
  const res = http.post(
    `${urls.customer}/customers`,
    JSON.stringify({ email, country: COUNTRIES() }),
    tagged('create_customer'),
  );
  check(res, { 'customer created': (r) => r.status === 201, 'no server error': (r) => r.status < 500 });
  if (res.status !== 201) {
    return null;
  }

  const id = res.json('id');
  knownCustomers.push(id);
  return id;
}

function clickAd(userId, campaigns) {
  const source = weighted(CAMPAIGN_WEIGHTS)();
  const campaign = campaigns.find((c) => c.utm_source === source) || pick(campaigns);
  const res = http.post(
    `${urls.marketing}/attributions`,
    JSON.stringify({ user_id: userId, utm_source: campaign.utm_source, utm_campaign: campaign.utm_campaign }),
    tagged('click_ad'),
  );
  check(res, { 'ad click logged': (r) => r.status === 201, 'no server error': (r) => r.status < 500 });
  return res.status === 201 ? res.json('attribution_id') : null;
}

function checkout(userId, productId, attributionId) {
  const body = { user_id: userId, product_id: productId, quantity: Number(QUANTITIES()) };
  if (attributionId) {
    body.attribution_id = attributionId;
  }
  const res = http.post(`${urls.order}/orders/checkout`, JSON.stringify(body), tagged('checkout'));
  check(res, {
    'checkout completed or failed cleanly': (r) => r.status === 201 || r.status === 422,
    'no server error': (r) => r.status < 500,
  });
}

function changeLifecycle(userId) {
  // Mostly upgrades, sometimes a suspension (whose checkouts then fail).
  const change = Math.random() < 0.8 ? { loyalty_tier: pick(['SILVER', 'GOLD']) } : { status: 'SUSPENDED' };
  const res = http.patch(`${urls.customer}/customers/${userId}`, JSON.stringify(change), tagged('lifecycle'));
  check(res, { 'no server error': (r) => r.status < 500 });
}

function restock(productId) {
  const res = http.post(
    `${urls.inventory}/inventory/restock`,
    JSON.stringify({ product_id: productId, quantity: 100 }),
    tagged('restock'),
  );
  check(res, { 'no server error': (r) => r.status < 500 });
}

// popularProduct skews demand: the first products in the catalog sell far
// more often than the rest, so depletion and stock-outs concentrate on them.
function popularProduct(products) {
  const index = Math.floor(Math.pow(Math.random(), 2.5) * products.length);
  return products[index];
}

function pick(items) {
  return items[Math.floor(Math.random() * items.length)];
}

// weighted returns a function that picks a key with probability proportional
// to its weight.
function weighted(weights) {
  const entries = Object.entries(weights);
  const total = entries.reduce((sum, [, w]) => sum + w, 0);
  return () => {
    let n = Math.random() * total;
    for (const [key, w] of entries) {
      if (n < w) {
        return key;
      }
      n -= w;
    }
    return entries[entries.length - 1][0];
  };
}

function tagged(name) {
  return { ...JSON_HEADERS, tags: { name } };
}
