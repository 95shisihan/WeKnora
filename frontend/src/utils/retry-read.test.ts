import assert from 'node:assert/strict'
import test from 'node:test'
import { retryRead } from './retry-read'

test('recovers a transient read failure', async () => {
  let calls = 0
  const result = await retryRead(async () => {
    if (++calls === 1) throw { code: 'ERR_NETWORK' }
    return 'loaded'
  })
  assert.equal(result, 'loaded')
  assert.equal(calls, 2)
})

test('does not retry permission or application errors', async () => {
  let calls = 0
  const error = { status: 403 }
  await assert.rejects(retryRead(async () => { calls++; throw error }), (actual) => actual === error)
  assert.equal(calls, 1)
})

test('stops after one retry and preserves transport diagnostics', async () => {
  let calls = 0
  const error = { code: 'ECONNABORTED', url: '/api/v1/knowledge-bases/example' }
  await assert.rejects(retryRead(async () => { calls++; throw error }), (actual) => actual === error)
  assert.equal(calls, 2)
})
