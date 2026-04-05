import http from 'k6/http';
import { check, sleep } from 'k6';

const baseURL = __ENV.IRONGATE_BASE_URL || 'http://127.0.0.1:8080';

export const options = {
  vus: 1,
  iterations: 5,
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<1000'],
  },
};

export function setup() {
  const loginResponse = http.post(`${baseURL}/api/users/login`, '');
  check(loginResponse, {
    'login status is 200': (response) => response.status === 200,
    'login returns token': (response) => Boolean(response.json('token')),
  });

  return {
    token: loginResponse.json('token'),
  };
}

export default function (data) {
  const authHeaders = {
    headers: {
      Authorization: `Bearer ${data.token}`,
    },
  };

  const health = http.get(`${baseURL}/health`);
  check(health, {
    'health is live': (response) => response.status === 200,
  });

  const ready = http.get(`${baseURL}/ready`);
  check(ready, {
    'ready is healthy': (response) => response.status === 200,
  });

  const users = http.get(`${baseURL}/api/users`, authHeaders);
  check(users, {
    'users route succeeds': (response) => response.status === 200,
  });

  const orders = http.get(`${baseURL}/api/orders`, authHeaders);
  check(orders, {
    'orders route succeeds': (response) => response.status === 200,
  });

  sleep(1);
}
