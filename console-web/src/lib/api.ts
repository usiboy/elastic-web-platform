export type Role = 'owner' | 'developer' | 'viewer' | 'agent'

export interface Identity {
  teamId: string
  role: Role
  projectId?: string
  actions: string[]
  expiresAt: string
}

export interface Project {
  id: string
  teamId: string
  slug: string
  name: string
  status: 'not_deployed' | 'active'
}

export interface Page<T> {
  hasMore: boolean
  nextCursor?: string
  projects?: T[]
  deployments?: T[]
}

export interface Deployment {
  deploymentId: string
  desiredHost?: string
  desiredState: string
  observedState: 'not_deployed' | 'preview_ready'
  sourceKind: string
  sourceId?: string
  createdAt: string
}

export interface GitPush {
  deliveryId: string
  commitSha: string
  ref: string
  status: 'pending_commit_verification' | 'awaiting_isolated_build' | 'preview_candidate' | 'production_requested' | 'superseded' | 'failed'
  attempts: number
  origin: 'webhook' | 'app_poll' | 'actions_oidc'
  receivedAt: string
  errorCode?: string
  sourceId?: string
  deploymentId?: string
  sourceBytes?: number
  buildStatus?: 'issued' | 'completed' | 'expired' | 'failed'
  builderId?: string
  buildIssuedAt?: string
  buildExpiresAt?: string
  buildAcceptedAt?: string
}

export interface ProjectActivity { action: string; subjectId: string; at: string }
export interface ProjectUsage {
  verifiedSources: number
  sourceArchiveBytes: number
  acceptedBuilds: number
  requestCount: null
  cpuTimeMs: null
  persistentStorageBytes: null
}

export interface Alias {
  desiredHost: string
  deploymentId: string
  desiredGeneration: number
  observedGeneration: number
  state: 'requested' | 'active'
}

export interface ResourceAmount {
  cpuMilli: number
  memoryMiB: number
  storageMiB: number
}

export interface TeamResources {
  teamId: string
  version: number
  budget: ResourceAmount
  reserved: ResourceAmount
  available: ResourceAmount
  state: 'budgeted_not_provisioned'
}

export interface ProjectResources {
  teamId: string
  projectId: string
  version: number
  resources: ResourceAmount
  state: 'unallocated' | 'reserved_not_provisioned'
}

export interface GitBinding {
  installationId: number
  repositoryId: number
  fullName: string
  defaultBranch: string
  status: 'pending' | 'verified' | 'revoked'
}

export class ApiError extends Error {
  constructor(public status: number, public code: string) {
    super(code)
    this.name = 'ApiError'
  }
}

type Fetcher = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>

export function createApi(token: string, base = '/v1', fetcher: Fetcher = fetch) {
  const root = base.replace(/\/$/, '')
  async function request<T>(method: string, path: string, body?: BodyInit, key?: string, contentType?: string): Promise<T> {
    const headers: Record<string, string> = { Authorization: `Bearer ${token}` }
    if (key) headers['Idempotency-Key'] = key
    if (contentType) headers['Content-Type'] = contentType
    const response = await fetcher(root + path, { method, headers, body, credentials: 'omit', redirect: 'error' })
    const result: unknown = await response.json().catch(() => ({}))
    if (!response.ok) {
      const error = result as { error?: { code?: string } }
      throw new ApiError(response.status, error?.error?.code || 'unexpected_response')
    }
    return result as T
  }

  const teamPath = (teamId: string) => `/teams/${encodeURIComponent(teamId)}`
  const projectPath = (teamId: string, projectId: string) => `${teamPath(teamId)}/projects/${encodeURIComponent(projectId)}`
  const deploymentPath = (teamId: string, projectId: string, deploymentId: string) =>
    `${projectPath(teamId, projectId)}/deployments/${encodeURIComponent(deploymentId)}`
  const optional = async <T>(task: Promise<T>): Promise<T | null> => {
    try { return await task } catch (error) {
      if (error instanceof ApiError && error.status === 404) return null
      throw error
    }
  }

  return {
    me: () => request<Identity>('GET', '/me'),
    projects: (teamId: string, cursor?: string) => request<{ projects: Project[]; hasMore: boolean; nextCursor?: string }>(
      'GET', `${teamPath(teamId)}/projects${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`,
    ),
    createProject: (teamId: string, name: string, slug: string) => request<Project>(
      'POST', `${teamPath(teamId)}/projects`, JSON.stringify({ name, slug }), crypto.randomUUID(), 'application/json',
    ),
    project: (teamId: string, projectId: string) => request<Project>('GET', projectPath(teamId, projectId)),
    githubPushes: (teamId: string, projectId: string, cursor?: string) => request<{ pushes: GitPush[]; hasMore: boolean; nextCursor?: string }>(
      'GET', `${projectPath(teamId, projectId)}/github-pushes${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`,
    ),
    activity: (teamId: string, projectId: string) => request<{ events: ProjectActivity[] }>('GET', `${projectPath(teamId, projectId)}/activity`),
    usage: (teamId: string, projectId: string) => request<ProjectUsage>('GET', `${projectPath(teamId, projectId)}/usage`),
    teamResources: (teamId: string) => optional(request<TeamResources>('GET', `${teamPath(teamId)}/resources`)),
    projectResources: (teamId: string, projectId: string) => request<ProjectResources>('GET', `${projectPath(teamId, projectId)}/resources`),
    reserveProjectResources: (teamId: string, projectId: string, expectedVersion: number, resources: ResourceAmount) =>
      request<ProjectResources>('PUT', `${projectPath(teamId, projectId)}/resources`,
        JSON.stringify({ expectedVersion, ...resources }), undefined, 'application/json'),
    deployments: (teamId: string, projectId: string, cursor?: string) => request<{
      deployments: Deployment[]; hasMore: boolean; nextCursor?: string
    }>('GET', `${projectPath(teamId, projectId)}/deployments${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`),
    production: (teamId: string, projectId: string) => optional(request<Alias>('GET', `${projectPath(teamId, projectId)}/aliases/production`)),
    githubBinding: (teamId: string, projectId: string) => optional(request<GitBinding>('GET', `${projectPath(teamId, projectId)}/github-binding`)),
    githubConnect: (teamId: string, projectId: string, installationId: number, repositoryId: number) => request<{
      authorizationUrl: string; expiresAt: string
    }>('POST', `${projectPath(teamId, projectId)}/github-connect`, JSON.stringify({ installationId, repositoryId }), undefined, 'application/json'),
    uploadSource: (teamId: string, projectId: string, file: File) => request<{ sourceId: string; status: string }>(
      'POST', `${projectPath(teamId, projectId)}/sources/uploads`, file, crypto.randomUUID(), 'application/gzip',
    ),
    createDeployment: (teamId: string, projectId: string, sourceId: string) => request<{
      deploymentId: string; desiredHost: string; observedState: string; statusUrl: string
    }>('POST', `${projectPath(teamId, projectId)}/deployments`, JSON.stringify({ sourceId }), crypto.randomUUID(), 'application/json'),
    previewExchange: (teamId: string, projectId: string, deploymentId: string) => request<{
      code: string; desiredHost: string; expiresAt: string
    }>('POST', `${deploymentPath(teamId, projectId, deploymentId)}/preview-exchanges`),
    promote: (teamId: string, projectId: string, deploymentId: string, expectedGeneration: number, rollback: boolean) => request<{
      desiredGeneration: number; observedGeneration: number; state: string
    }>('POST', `${projectPath(teamId, projectId)}/aliases/production:${rollback ? 'rollback' : 'promote'}`,
      JSON.stringify({ deploymentId, expectedGeneration }), undefined, 'application/json'),
  }
}
