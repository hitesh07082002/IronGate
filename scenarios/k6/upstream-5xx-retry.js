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
  const response = http.get(`${baseURL}/api/orders`, { headers });

  check(response, {
    'upstream 5xx retry response observed': (res) => [200, 502, 503].includes(res.status),
  });
}
