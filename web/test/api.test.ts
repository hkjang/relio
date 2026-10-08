import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { once } from 'node:events'
import { api, APIError } from '../src/api.ts'

// Exercise the shared client through real HTTP and fetch, including body parsing.
// Only the remote response is controlled; the client and its dependencies are real.
const cases = [
  { name: 'HTML success', status: 200, body: '<html>Proxy error</html>', code: 'invalid_response' },
  { name: 'empty success', status: 200, body: '', code: 'invalid_response' },
  { name: 'truncated creation response', status: 201, body: '{"id":', code: 'invalid_response' },
  { name: 'non-JSON gateway error', status: 502, body: '<html>Bad gateway</html>', code: 'request_failed' },
  { name: 'empty error', status: 400, body: '', code: 'request_failed' },
  { name: 'structured API error', status: 403, body: '{"error":{"code":"forbidden","message":"권한이 없습니다."}}', code: 'forbidden', message: '권한이 없습니다.' },
  { name: 'JSON object', status: 200, body: '{"items":[{"id":"customer-1"}]}', value: { items: [{ id: 'customer-1' }] } },
  { name: 'created JSON object', status: 201, body: '{"id":"customer-1"}', value: { id: 'customer-1' } },
  { name: 'JSON array', status: 200, body: '[]', value: [] },
  { name: 'JSON null', status: 200, body: 'null', value: null },
  { name: 'no content', status: 204, body: '', value: undefined },
]

for (const scenario of cases) {
  test(`api handles ${scenario.name} over HTTP`, async t => {
    const server = createServer((_request, response) => {
      response.writeHead(scenario.status)
      response.end(scenario.body)
    })
    t.after(() => new Promise<void>((resolve, reject) => {
      server.close(error => error ? reject(error) : resolve())
      server.closeAllConnections()
    }))
    server.listen(0, '127.0.0.1')
    await once(server, 'listening')
    const address = server.address()
    assert.ok(address && typeof address !== 'string')

    const result = api(`http://127.0.0.1:${address.port}/api/v1/customers`)
    if (scenario.code) {
      await assert.rejects(result, (error: unknown) => {
        assert.ok(error instanceof APIError)
        assert.equal(error.status, scenario.status)
        assert.equal(error.code, scenario.code)
        assert.equal(error.message, scenario.message ?? (scenario.code === 'invalid_response'
          ? '서버 응답을 읽을 수 없습니다. 처리 결과를 확인해 주세요.'
          : `요청 실패 (${scenario.status})`))
        return true
      })
    } else {
      assert.deepEqual(await result, scenario.value)
    }
  })
}
