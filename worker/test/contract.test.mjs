import assert from 'node:assert/strict';
import { test } from 'node:test';
import { parseClaim, parseAllowedOrigins } from '../src/contract.ts';

const claim = steps => ({ run: { id: 'run', base_url: 'http://127.0.0.1:4174', scenarios: [{ id: 'scenario', steps }] }, lease_token: 'lease', lease_expires_at: new Date().toISOString() });

test('worker rejects claims without a fixed approved assertion', () => {
  assert.throws(() => parseClaim(claim([{ action: 'navigate', path: '/' }])), /assertion/);
});

test('worker rejects unsafe navigation and extraneous step fields', () => {
  assert.throws(() => parseClaim(claim([{ action: 'navigate', path: '//evil.test' }, { action: 'assert_visible', test_id: 'ok' }])), /navigation/);
  assert.throws(() => parseClaim(claim([{ action: 'navigate', path: '/x?secret=1' }, { action: 'assert_visible', test_id: 'ok' }])), /navigation/);
  assert.throws(() => parseClaim(claim([{ action: 'click', test_id: 'ok', value: 'extra' }, { action: 'assert_visible', test_id: 'ok' }])), /fields/);
});

test('allowed origins must be exact HTTP(S) origins', () => {
  assert.deepEqual([...parseAllowedOrigins('http://127.0.0.1:4174,https://sample.test')], ['http://127.0.0.1:4174', 'https://sample.test']);
  assert.throws(() => parseAllowedOrigins('http://127.0.0.1:4174/path'));
  assert.throws(() => parseAllowedOrigins('https://user:password@sample.test'));
});
