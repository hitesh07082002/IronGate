import http from 'k6/http';
import { check } from 'k6';
import { buildOptions, scenarioHeaders, setupGateway } from './lib/common.js';

export const options = buildOptions();

const badAuthCases = [
  { name: 'missing_header', headers: {} },
  { name: 'malformed_token', headers: { Authorization: 'Bearer not-a-jwt' } },
  {
    name: 'wrong_algorithm',
    headers: {
      Authorization: 'Bearer eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.eyJzdWIiOiJub25lLXVzZXIiLCJyb2xlIjoiYWRtaW4iLCJpYXQiOjE1MTYyMzkwMjIsImV4cCI6MjUzNDAyMzAwNzk5fQ.',
    },
  },
  {
    name: 'invalid_signature',
    headers: {
      Authorization: 'Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJiYWQtdXNlciIsInJvbGUiOiJhZG1pbiIsImlhdCI6MTUxNjIzOTAyMiwiZXhwIjoyNTM0MDIzMDA3OTl9.invalid-signature',
    },
  },
  {
    name: 'expired_like',
    headers: {
      Authorization: 'Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJleHBpcmVkLXVzZXIiLCJyb2xlIjoiYWRtaW4iLCJpYXQiOjE1MTYyMzkwMjIsImV4cCI6MTUxNjIzOTEyMn0.invalid-signature',
    },
  },
];

export function setup() {
  return setupGateway({ headers: {}, tokenPoolSize: 0 });
}

function mergedHeaders(baseHeaders, overrideHeaders) {
  const headers = {};
  const sources = [baseHeaders || {}, overrideHeaders || {}];

  for (let sourceIndex = 0; sourceIndex < sources.length; sourceIndex += 1) {
    const source = sources[sourceIndex];
    const keys = Object.keys(source);
    for (let keyIndex = 0; keyIndex < keys.length; keyIndex += 1) {
      const key = keys[keyIndex];
      headers[key] = source[key];
    }
  }

  return headers;
}

export default function (data) {
  const baseURL = (data && data.baseURL) || (__ENV.TARGET_URL || 'http://gateway:8080');
  const baseHeaders = scenarioHeaders(data);
  const authCase = badAuthCases[(__ITER + __VU - 1) % badAuthCases.length];
  const response = http.get(`${baseURL}/api/users`, {
    headers: mergedHeaders(baseHeaders, authCase.headers),
  });

  check(response, {
    'auth wall rejects request': (res) => res.status === 401,
  });
}
