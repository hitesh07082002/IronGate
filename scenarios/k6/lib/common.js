import http from 'k6/http';
import { check } from 'k6';

export function baseURL() {
  return __ENV.TARGET_URL || 'http://gateway:8080';
}

export function authHeaders(token) {
  const jwt = token || __ENV.JWT || '';
  if (!jwt) {
    return {};
  }

  return {
    Authorization: `Bearer ${jwt}`,
  };
}

function tokenPoolSize(override) {
  const rawValue = override !== undefined ? override : (__ENV.TOKEN_POOL_SIZE || 0);
  const value = Number(rawValue);
  if (!isFinite(value) || value <= 0) {
    return 0;
  }

  return Math.floor(value);
}

function loginPayload(index) {
  return JSON.stringify({
    subject: `${__ENV.LOGIN_SUBJECT_PREFIX || 'observatory-user'}-${index}`,
    role: __ENV.LOGIN_ROLE || 'admin',
  });
}

function bootstrapToken(base, index) {
  const response = http.post(`${base}/api/users/login`, loginPayload(index), {
    headers: {
      'Content-Type': 'application/json',
    },
  });

  check(response, {
    'scenario login ok': (res) => res.status === 200,
  });
  if (response.status !== 200) {
    throw new Error(`login bootstrap failed with status ${response.status}`);
  }

  const payload = response.json();
  if (!payload || !payload.token) {
    throw new Error('login bootstrap returned no token');
  }

  return String(payload.token);
}

function buildTokenPool(base, overrideSize) {
  const size = tokenPoolSize(overrideSize);
  if (size === 0) {
    return [];
  }

  const tokens = [];
  for (let index = 0; index < size; index += 1) {
    tokens.push(bootstrapToken(base, index));
  }

  return tokens;
}

export function scenarioHeaders(data) {
  if (data && data.tokenPool && data.tokenPool.length > 0) {
    const index = (__ITER + __VU - 1) % data.tokenPool.length;
    return authHeaders(data.tokenPool[index]);
  }
  if (data && data.headers) {
    return data.headers;
  }

  return authHeaders();
}

export function buildOptions() {
  const rate = Number(__ENV.RPS || 10);
  const durationSeconds = Number(__ENV.DURATION || 30);
  const preAllocatedVUs = Math.max(10, Math.ceil(rate / 2));
  const maxVUs = Math.max(50, rate * 2);

  return {
    scenarios: {
      default: {
        executor: 'constant-arrival-rate',
        rate,
        timeUnit: '1s',
        duration: `${durationSeconds}s`,
        preAllocatedVUs,
        maxVUs,
      },
    },
  };
}

export function setupGateway(options = {}) {
  const gatewayBaseURL = baseURL();
  const response = http.get(`${gatewayBaseURL}/health`);
  check(response, {
    'gateway reachable': (res) => res.status === 200,
  });

  const tokenPool = buildTokenPool(gatewayBaseURL, options.tokenPoolSize);
  let headers;
  if (Object.prototype.hasOwnProperty.call(options, 'headers')) {
    headers = options.headers;
  } else if (tokenPool.length > 0) {
    headers = {};
  } else {
    headers = authHeaders();
  }

  return {
    baseURL: gatewayBaseURL,
    headers,
    tokenPool,
  };
}
