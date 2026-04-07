import http from 'k6/http';
import { check } from 'k6';
import { buildOptions, scenarioHeaders, setupGateway } from './lib/common.js';

export const options = buildOptions();

export function setup() {
  return setupGateway({ tokenPoolSize: 50 });
}

export default function (data) {
  const headers = scenarioHeaders(data);
  const baseURL = (data && data.baseURL) || (__ENV.TARGET_URL || 'http://gateway:8080');
  const response = http.get(`${baseURL}/api/orders`, { headers });

  check(response, {
    'many key rate limit response observed': (res) => [200, 429].includes(res.status),
  });
}
