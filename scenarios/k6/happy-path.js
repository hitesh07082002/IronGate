import http from 'k6/http';
import { check } from 'k6';
import { buildOptions, scenarioHeaders, setupGateway } from './lib/common.js';

export const options = buildOptions();

export function setup() {
  return setupGateway();
}

export default function (data) {
  const headers = scenarioHeaders(data);
  const baseURL = (data && data.baseURL) || (__ENV.TARGET_URL || 'http://gateway:8080');
  const path = (__ITER % 2 === 0) ? '/api/users' : '/api/orders';
  const response = http.get(`${baseURL}${path}`, { headers });

  check(response, {
    'happy path status ok': (res) => res.status === 200,
  });
}
