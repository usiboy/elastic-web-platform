import { describe, expect, it, vi } from 'vitest'
import { createApi } from './api'

describe('project control API', () => {
  it('reads scoped audit transitions and measured source bytes without fabricating request usage', async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ events: [{ action: 'github.build_job_failed', subjectId: 'job_a', at: '2026-10-08T14:00:00Z' }] }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ verifiedSources: 1, sourceArchiveBytes: 2048, acceptedBuilds: 0,
        requestCount: null, cpuTimeMs: null, persistentStorageBytes: null }), { status: 200 }))
    const api = createApi('ewp_owner', '/v1', fetcher)
    expect((await api.activity('t_alpha', 'p_alpha')).events[0].action).toBe('github.build_job_failed')
    const usage = await api.usage('t_alpha', 'p_alpha')
    expect(usage.sourceArchiveBytes).toBe(2048)
    expect(usage.cpuTimeMs).toBeNull()
    expect(fetcher).toHaveBeenNthCalledWith(1, '/v1/teams/t_alpha/projects/p_alpha/activity', expect.objectContaining({ method: 'GET' }))
    expect(fetcher).toHaveBeenNthCalledWith(2, '/v1/teams/t_alpha/projects/p_alpha/usage', expect.objectContaining({ method: 'GET' }))
  })
  it('reads real, paginated Git push states without treating a blocked build as a deployment', async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      pushes: [{ deliveryId: 'poll-fixed', commitSha: 'a'.repeat(40), ref: 'refs/heads/main',
        status: 'awaiting_isolated_build', origin: 'app_poll', receivedAt: '2026-10-06T00:00:00Z', attempts: 0 }],
      hasMore: true, nextCursor: 'opaque-cursor',
    }), { status: 200 }))
    const api = createApi('ewp_owner', '/v1', fetcher)
    const page = await api.githubPushes('t_alpha', 'p_alpha', 'older-cursor')
    expect(page.pushes[0].status).toBe('awaiting_isolated_build')
    expect(page.pushes[0].deploymentId).toBeUndefined()
    expect(fetcher).toHaveBeenCalledWith('/v1/teams/t_alpha/projects/p_alpha/github-pushes?cursor=older-cursor',
      expect.objectContaining({ method: 'GET', credentials: 'omit', headers: expect.objectContaining({ Authorization: 'Bearer ewp_owner' }) }))
  })
  it('keeps reservations scoped to Team/Project with CAS and does not call an operator budget endpoint', async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      projectId: 'p_alpha', version: 2, state: 'reserved_not_provisioned',
      resources: { cpuMilli: 500, memoryMiB: 512, storageMiB: 256 },
    }), { status: 200 }))
    const api = createApi('ewp_owner', '/v1', fetcher)
    const result = await api.reserveProjectResources('t_alpha', 'p_alpha', 1, { cpuMilli: 500, memoryMiB: 512, storageMiB: 256 })
    expect(result.state).toBe('reserved_not_provisioned')
    expect(fetcher).toHaveBeenCalledWith('/v1/teams/t_alpha/projects/p_alpha/resources', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ expectedVersion: 1, cpuMilli: 500, memoryMiB: 512, storageMiB: 256 }),
      headers: expect.objectContaining({ Authorization: 'Bearer ewp_owner', 'Content-Type': 'application/json' }),
    }))
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
  it('sends a scoped bearer only to the control API and preserves real empty status', async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      projects: [{ id: 'p_alpha', teamId: 't_alpha', slug: 'first-site', name: 'First Site', status: 'not_deployed' }],
      hasMore: false,
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    const api = createApi('ewp_owner', '/v1', fetcher)
    const list = await api.projects('t_alpha')
    expect(list.projects[0].status).toBe('not_deployed')
    expect(fetcher).toHaveBeenCalledWith('/v1/teams/t_alpha/projects', expect.objectContaining({
      headers: expect.objectContaining({ Authorization: 'Bearer ewp_owner' }),
    }))
  })

  it('does not claim a requested Alias is active', async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      desiredHost: 'p-alpha.sites.test', deploymentId: 'dep_one', desiredGeneration: 2,
      observedGeneration: 1, state: 'requested',
    }), { status: 200 }))
    const alias = await createApi('ewp_owner', '/v1', fetcher).production('t_alpha', 'p_alpha')
    expect(alias?.state).toBe('requested')
    expect(alias?.observedGeneration).toBe(1)
  })

  it('treats a missing production Alias as an empty state and denies unrelated errors', async () => {
    const response = new Response(JSON.stringify({ error: { code: 'not_found' } }), { status: 404 })
    const api = createApi('ewp_owner', '/v1', vi.fn().mockResolvedValue(response))
    expect(await api.production('t_alpha', 'p_alpha')).toBeNull()
    const broken = createApi('ewp_owner', '/v1', vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: { code: 'database_unavailable' } }), { status: 503 })))
    await expect(broken.production('t_alpha', 'p_alpha')).rejects.toThrow('database_unavailable')
  })
})
