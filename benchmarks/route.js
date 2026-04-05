import http from 'k6/http';
import { sleep } from 'k6';
import { Counter } from 'k6/metrics';

const baseURL = (__ENV.IRONGATE_BASE_URL || 'http://127.0.0.1:8080').replace(/\/$/, '');
const loginPath = (__ENV.IRONGATE_LOGIN_PATH || '/api/users/login').trim();
const requestPath = (__ENV.IRONGATE_ROUTE_PATH || '/health').trim();
const requestMethod = (__ENV.IRONGATE_METHOD || 'GET').toUpperCase();
const requestBody = __ENV.IRONGATE_REQUEST_BODY || '';
const authMode = (__ENV.IRONGATE_AUTH_MODE || 'none').trim();
const xffMode = (__ENV.IRONGATE_XFF_MODE || 'none').trim();
const loginRole = (__ENV.IRONGATE_LOGIN_ROLE || 'user').trim();
const subjectPrefix = (__ENV.IRONGATE_LOGIN_SUBJECT_PREFIX || 'bench-user').trim();
const authPoolSize = parseInteger(__ENV.IRONGATE_AUTH_POOL_SIZE, 1);
const sleepMS = parseInteger(__ENV.IRONGATE_SLEEP_MS, 0);
const expectedStatuses = parseIntegerList(__ENV.IRONGATE_EXPECTED_STATUSES || '200');
const tokenPoolPath = (__ENV.IRONGATE_TOKEN_POOL_PATH || '').trim();
const preloadedTokens = tokenPoolPath !== '' ? JSON.parse(open(tokenPoolPath)) : [];

http.setResponseCallback(http.expectedStatuses(...expectedStatuses));

export const options = {
  scenarios: {
    main: buildScenario(),
  },
  summaryTrendStats: ['avg', 'p(50)', 'p(95)', 'p(99)'],
};

const status200 = new Counter('irongate_status_200');
const status429 = new Counter('irongate_status_429');
const status500 = new Counter('irongate_status_500');
const status503 = new Counter('irongate_status_503');
const statusOther = new Counter('irongate_status_other');
const unexpectedStatus = new Counter('irongate_unexpected_status');

export function setup() {
  if (preloadedTokens.length > 0) {
    return {
      tokens: preloadedTokens,
    };
  }

  if (authMode === 'none') {
    return { tokens: [] };
  }

  if (authMode === 'static') {
    return {
      tokens: [mintLoginToken(`${subjectPrefix}-0`, loginRole, 0)],
    };
  }

  if (authMode === 'pool') {
    const tokens = [];
    for (let index = 0; index < authPoolSize; index += 1) {
      tokens.push(mintLoginToken(`${subjectPrefix}-${index}`, loginRole, index));
    }
    return { tokens };
  }

  throw new Error(`unsupported auth mode: ${authMode}`);
}

export default function (data) {
  const headers = {};
  const token = pickToken(data);
  if (token) {
    headers.Authorization = `Bearer ${token}`;
  }
  if (xffMode === 'per_request') {
    headers['X-Forwarded-For'] = benchmarkIP((__VU * 100000) + __ITER);
  }

  const params = {
    headers,
    tags: {
      name: requestPath,
      irongate_scenario: __ENV.IRONGATE_SCENARIO_NAME || requestPath,
    },
  };

  const response = http.request(requestMethod, `${baseURL}${requestPath}`, bodyForMethod(requestMethod), params);
  recordStatus(response.status);

  if (expectedStatuses.indexOf(response.status) === -1) {
    unexpectedStatus.add(1);
  }

  if (sleepMS > 0) {
    sleep(sleepMS / 1000);
  }
}

function buildScenario() {
  const vus = Math.max(parseInteger(__ENV.IRONGATE_VUS, 1), 1);
  const duration = (__ENV.IRONGATE_DURATION || '').trim();
  const iterations = parseInteger(__ENV.IRONGATE_ITERATIONS, 0);

  if (duration !== '') {
    return {
      executor: 'constant-vus',
      vus,
      duration,
    };
  }

  return {
    executor: 'shared-iterations',
    vus,
    iterations: Math.max(iterations, vus),
  };
}

function pickToken(data) {
  const tokens = data && Array.isArray(data.tokens) ? data.tokens : [];
  if (tokens.length === 0) {
    return '';
  }

  return tokens[((__VU - 1) + __ITER) % tokens.length];
}

function mintLoginToken(subject, role, seed) {
  const headers = {
    'Content-Type': 'application/json',
    'X-Forwarded-For': benchmarkIP(seed),
  };
  const response = http.post(
    `${baseURL}${loginPath}`,
    JSON.stringify({ subject, role }),
    { headers, tags: { name: loginPath, irongate_flow: 'setup-login' } },
  );

  if (response.status !== 200) {
    throw new Error(`login failed for ${subject}: status=${response.status} body=${response.body}`);
  }

  const token = response.json('token');
  if (!token) {
    throw new Error(`login did not return token for ${subject}`);
  }

  return token;
}

function recordStatus(statusCode) {
  switch (statusCode) {
    case 200:
      status200.add(1);
      break;
    case 429:
      status429.add(1);
      break;
    case 500:
      status500.add(1);
      break;
    case 503:
      status503.add(1);
      break;
    default:
      statusOther.add(1);
      break;
  }
}

function bodyForMethod(method) {
  if (method === 'GET' || method === 'HEAD') {
    return null;
  }

  if (requestBody === '') {
    return null;
  }

  return requestBody;
}

function benchmarkIP(seed) {
  const normalized = Math.abs(Number(seed) || 0);
  const third = Math.floor(normalized / 250) % 250;
  const fourth = normalized % 250;
  return `198.18.${third + 1}.${fourth + 1}`;
}

function parseInteger(value, fallbackValue) {
  const parsed = parseInt(`${value || ''}`, 10);
  if (Number.isNaN(parsed)) {
    return fallbackValue;
  }

  return parsed;
}

function parseIntegerList(value) {
  return `${value}`
    .split(',')
    .map((item) => parseInteger(item.trim(), NaN))
    .filter((item) => !Number.isNaN(item));
}
