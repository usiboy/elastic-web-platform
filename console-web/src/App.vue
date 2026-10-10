<script setup lang="ts">
import { computed, ref, shallowRef } from 'vue'
import DeploymentLine from './components/DeploymentLine.vue'
import StatusPill from './components/StatusPill.vue'
import { ApiError, createApi, type Alias, type Deployment, type GitBinding, type GitPush, type Identity, type Project, type ProjectActivity, type ProjectResources, type ProjectUsage, type TeamResources } from './lib/api'

type Api = ReturnType<typeof createApi>
type Tab = 'overview' | 'deployments' | 'settings'

const teamInput = ref('')
const tokenInput = ref('')
const identity = ref<Identity | null>(null)
const api = shallowRef<Api | null>(null)
const projects = ref<Project[]>([])
const projectCursor = ref<string | null>(null)
const selected = ref<Project | null>(null)
const deployments = ref<Deployment[]>([])
const deploymentCursor = ref<string | null>(null)
const production = ref<Alias | null>(null)
const github = ref<GitBinding | null>(null)
const gitPushes = ref<GitPush[]>([])
const gitPushCursor = ref<string | null>(null)
const activity = ref<ProjectActivity[]>([])
const usage = ref<ProjectUsage | null>(null)
const projectResources = ref<ProjectResources | null>(null)
const teamResources = ref<TeamResources | null>(null)
const cpuMilli = ref(0)
const memoryMiB = ref(0)
const storageMiB = ref(0)
const activeTab = ref<Tab>('overview')
const busy = ref(false)
const loadingProject = ref(false)
const projectProblem = ref('')
const error = ref('')
const message = ref('')
const createOpen = ref(false)
const newName = ref('')
const newSlug = ref('')
const sourceFile = ref<File | null>(null)
const installationID = ref('')
const repositoryID = ref('')

const canCreate = computed(() => identity.value?.role === 'owner' || identity.value?.role === 'developer')
const canDeploy = computed(() => canCreate.value || (identity.value?.role === 'agent' && identity.value.actions.includes('sources:upload') && identity.value.actions.includes('deployments:create')))
const canPromote = computed(() => identity.value?.role === 'owner')
const canGitConnect = computed(() => identity.value?.role === 'owner')
const activeProduction = computed(() => production.value?.state === 'active' ? production.value.deploymentId : '')
const siteSuffix = (import.meta.env.VITE_SITE_SUFFIX as string | undefined)?.trim().toLowerCase() || ''
const previewSuffix = (import.meta.env.VITE_PREVIEW_SUFFIX as string | undefined)?.trim().toLowerCase() || ''
const siteScheme = import.meta.env.VITE_SITE_SCHEME === 'http' ? 'http' : 'https'
const productionURL = computed(() => {
  if (production.value?.state !== 'active' || !siteSuffix || !production.value.desiredHost.endsWith(`.${siteSuffix}`)) return ''
  return `${siteScheme}://${production.value.desiredHost}/`
})

const explanation: Record<string, string> = {
  isolated_build_required: '这个项目需要隔离构建器；当前不会在控制机执行 Vite 或依赖脚本。',
  github_user_repo_denied: '当前 GitHub 用户没有目标仓库的读取权限。',
  github_oauth_unavailable: 'GitHub 用户授权尚未配置。',
  preview_not_observed: '先通过受保护的 Preview 确认站点已实际提供内容。',
  static_source_rejected: '静态源码包含隐藏文件、构建配置或不允许公开的路径。',
  alias_conflict: '生产版本已变化，请刷新后重试。',
  unauthenticated: '身份已过期或撤销，请重新连接工作台。',
  database_unavailable: '控制数据库暂不可用，状态无法核对。',
  resource_budget_unavailable: 'Team 尚未配置资源预算；请由平台运维先设置预算。',
  resource_budget_exceeded: '申请超过 Team 当前剩余额度，请缩小申请或联系运维。',
  resource_version_conflict: '资源预留已变化，请刷新项目后重试。',
  build_failed: '隔离构建失败，旧生产版本未切换。',
  build_timeout: '隔离构建超时，旧生产版本未切换。',
  build_output_invalid: '隔离构建的输出未通过校验，旧生产版本未切换。',
  build_job_expired: '构建作业已过期，请创建新的提交。',
}

function describeFailure(cause: unknown) {
  if (cause instanceof ApiError) return explanation[cause.code] || `请求未完成（${cause.code}）`
  return cause instanceof Error ? cause.message : '请求未完成，请稍后重试。'
}

async function work(action: () => Promise<void>) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  message.value = ''
  try { await action() } catch (cause) { error.value = describeFailure(cause) } finally { busy.value = false }
}

async function connect() {
  await work(async () => {
    const candidate = createApi(tokenInput.value.trim())
    const me = await candidate.me()
    if (me.teamId !== teamInput.value.trim()) throw new Error('Team ID 与当前令牌归属不一致。')
    const page = await candidate.projects(me.teamId)
    api.value = candidate
    identity.value = me
    projects.value = page.projects
    projectCursor.value = page.nextCursor || null
    tokenInput.value = '' // Bearer is held only inside the in-memory client closure.
    selected.value = null
    gitPushes.value = []
    gitPushCursor.value = null
    activity.value = []
    usage.value = null
  })
}

function disconnect() {
  api.value = null
  identity.value = null
  projects.value = []
  selected.value = null
  production.value = null
  deployments.value = []
  github.value = null
  gitPushes.value = []
  gitPushCursor.value = null
  activity.value = []
  usage.value = null
  projectResources.value = null
  teamResources.value = null
  gitPushes.value = []
  gitPushCursor.value = null
  tokenInput.value = ''
  error.value = ''
  message.value = ''
}

function showProjects(create = false) {
  selected.value = null
  activeTab.value = 'overview'
  createOpen.value = create
  projectProblem.value = ''
}

async function loadProjects(next = false) {
  if (!api.value || !identity.value) return
  const page = await api.value.projects(identity.value.teamId, next ? projectCursor.value || undefined : undefined)
  projects.value = next ? [...projects.value, ...page.projects] : page.projects
  projectCursor.value = page.nextCursor || null
  if (selected.value) {
    const updated = projects.value.find(project => project.id === selected.value?.id)
    if (updated) selected.value = updated
  }
}

async function chooseProject(project: Project) {
  selected.value = project
  projectResources.value = null
  teamResources.value = null
  activeTab.value = 'overview'
  await loadProject(project.id)
}

async function loadProject(id: string) {
  if (!identity.value || !api.value) return
  const client = api.value
  const team = identity.value.teamId
  loadingProject.value = true
  projectProblem.value = ''
  try {
    const [fresh, list, alias, binding, allocation, budget, pushPage, activityPage, usagePage] = await Promise.all([
      client.project(team, id), client.deployments(team, id), client.production(team, id), client.githubBinding(team, id),
      client.projectResources(team, id), identity.value.role === 'agent' ? Promise.resolve(null) : client.teamResources(team),
      client.githubPushes(team, id),
      client.activity(team, id), client.usage(team, id),
    ])
    if (selected.value?.id !== id) return
    selected.value = fresh
    projects.value = projects.value.map(project => project.id === id ? fresh : project)
    deployments.value = list.deployments
    deploymentCursor.value = list.nextCursor || null
    production.value = alias
    github.value = binding
    gitPushes.value = pushPage.pushes
    gitPushCursor.value = pushPage.nextCursor || null
    activity.value = activityPage.events
    usage.value = usagePage
    projectResources.value = allocation
    teamResources.value = budget
    cpuMilli.value = allocation.resources.cpuMilli
    memoryMiB.value = allocation.resources.memoryMiB
    storageMiB.value = allocation.resources.storageMiB
  } catch (cause) {
    if (selected.value?.id === id) projectProblem.value = describeFailure(cause)
  } finally { loadingProject.value = false }
}

async function createProject() {
  await work(async () => {
    if (!api.value || !identity.value || !canCreate.value) return
    const project = await api.value.createProject(identity.value.teamId, newName.value.trim(), newSlug.value.trim())
    createOpen.value = false
    newName.value = ''
    newSlug.value = ''
    await loadProjects()
    await chooseProject(project)
    message.value = '项目已创建；尚未有运行中的网站。'
  })
}

function selectFile(event: Event) {
  sourceFile.value = (event.target as HTMLInputElement).files?.[0] || null
}

async function deployStatic() {
  await work(async () => {
    if (!api.value || !identity.value || !selected.value || !sourceFile.value || !canDeploy.value) return
    if (sourceFile.value.size > 1 << 20) throw new Error('源码压缩包超过 1 MiB。')
    const team = identity.value.teamId
    const project = selected.value.id
    const source = await api.value.uploadSource(team, project, sourceFile.value)
    const deployment = await api.value.createDeployment(team, project, source.sourceId)
    sourceFile.value = null
    await loadProject(project)
    activeTab.value = 'deployments'
    message.value = `已登记 ${deployment.deploymentId}；等待实际 Preview 入口读回。`
  })
}

async function loadMoreDeployments() {
  await work(async () => {
    if (!api.value || !identity.value || !selected.value || !deploymentCursor.value) return
    const page = await api.value.deployments(identity.value.teamId, selected.value.id, deploymentCursor.value)
    deployments.value = [...deployments.value, ...page.deployments]
    deploymentCursor.value = page.nextCursor || null
  })
}

async function loadMoreGitPushes() {
  await work(async () => {
    if (!api.value || !identity.value || !selected.value || !gitPushCursor.value) return
    const team = identity.value.teamId
    const project = selected.value.id
    const cursor = gitPushCursor.value
    const page = await api.value.githubPushes(team, project, cursor)
    if (identity.value?.teamId !== team || selected.value?.id !== project || gitPushCursor.value !== cursor) return
    gitPushes.value = [...gitPushes.value, ...page.pushes]
    gitPushCursor.value = page.nextCursor || null
  })
}

async function promote(deployment: Deployment) {
  await work(async () => {
    if (!api.value || !identity.value || !selected.value || !canPromote.value) return
    const rollback = isRollbackTarget(deployment)
    await api.value.promote(identity.value.teamId, selected.value.id, deployment.deploymentId, production.value?.desiredGeneration || 0, !!rollback)
    await loadProject(selected.value.id)
    message.value = '生产切换意图已记录；网关实际读回之前不会标为生效。'
  })
}

async function openPreview(deployment: Deployment) {
  if (!api.value || !identity.value || !selected.value) return
  if (window.location.protocol !== 'https:' || !previewSuffix) {
    error.value = '浏览器 Preview 需要受信的 HTTPS 控制台与已配置的 Preview 域名。'
    return
  }
  const popup = window.open('about:blank', '_blank')
  if (!popup) { error.value = '浏览器阻止了预览窗口。'; return }
  popup.opener = null
  popup.name = `preview-${deployment.deploymentId}`
  await work(async () => {
    if (!api.value || !identity.value || !selected.value) return
    try {
      const exchange = await api.value.previewExchange(identity.value.teamId, selected.value.id, deployment.deploymentId)
      const host = exchange.desiredHost.toLowerCase()
      if (!/^d-[0-9a-f]{32}\.[a-z0-9.-]+$/.test(host) || !host.endsWith(`.${previewSuffix}`)) throw new Error('预览 Host 未通过平台域名校验。')
      const form = document.createElement('form')
      form.method = 'POST'
      form.action = `https://${host}/session/exchange`
      form.target = popup.name
      const input = document.createElement('input')
      input.type = 'hidden'
      input.name = 'code'
      input.value = exchange.code
      form.append(input)
      document.body.append(form)
      form.submit() // The one-time code is sent in the body, never placed in a URL.
      form.remove()
    } catch (cause) { popup.close(); throw cause }
  })
}

async function connectGitHub() {
  await work(async () => {
    if (!api.value || !identity.value || !selected.value || !canGitConnect.value) return
    const installationId = Number(installationID.value)
    const repositoryId = Number(repositoryID.value)
    if (!Number.isSafeInteger(installationId) || !Number.isSafeInteger(repositoryId) || installationId <= 0 || repositoryId <= 0) throw new Error('请输入有效的 Installation ID 与 Repository ID。')
    const grant = await api.value.githubConnect(identity.value.teamId, selected.value.id, installationId, repositoryId)
    const destination = new URL(grant.authorizationUrl)
    if (destination.origin !== 'https://github.com' || destination.pathname !== '/login/oauth/authorize') throw new Error('GitHub 授权地址不可信。')
    window.open(destination.toString(), '_blank', 'noopener,noreferrer')
    message.value = '请在 GitHub 完成授权，然后返回此页刷新来源状态。'
  })
}

async function reserveResources() {
  await work(async () => {
    if (!api.value || !identity.value || !selected.value || identity.value.role !== 'owner' || !projectResources.value || !teamResources.value) return
    const values = [cpuMilli.value, memoryMiB.value, storageMiB.value]
    if (!values.every(value => Number.isSafeInteger(value) && value >= 0)) throw new Error('资源申请必须填写非负整数。')
    const allZero = values.every(value => value === 0)
    if (!allZero && (cpuMilli.value < 100 || memoryMiB.value < 128 || storageMiB.value < 128)) throw new Error('非零申请需要至少 100m CPU、128 MiB 内存和 128 MiB 存储。')
    await api.value.reserveProjectResources(identity.value.teamId, selected.value.id, projectResources.value.version,
      { cpuMilli: cpuMilli.value, memoryMiB: memoryMiB.value, storageMiB: storageMiB.value })
    await loadProject(selected.value.id)
    message.value = '预留已记录；运行时尚未配给资源，也没有执行 CPU/存储硬限。'
  })
}

const latest = computed(() => deployments.value[0] || null)
function isRollbackTarget(deployment: Deployment) {
  const current = deployments.value.find(item => item.deploymentId === production.value?.deploymentId)
  return !!current && new Date(deployment.createdAt).getTime() < new Date(current.createdAt).getTime()
}
const knownGitSourceBytes = computed(() => usage.value?.sourceArchiveBytes || 0)
const verifiedBuilds = computed(() => usage.value?.acceptedBuilds || 0)
function buildState(push: GitPush) {
  if (push.buildStatus === 'completed') return '签名制品已验收'
  if (push.buildStatus === 'failed' || push.buildStatus === 'expired') return `作业失败 · ${push.errorCode || push.buildStatus}`
  if (push.buildStatus === 'issued') return '隔离构建作业已签发 · 等待产物'
  if (push.status === 'awaiting_isolated_build') return '已核源 · 尚无构建作业'
  if (push.status === 'preview_candidate') return '私有候选已创建 · 等待 Preview 实际读回'
  return push.status
}
</script>

<template>
  <div v-if="!identity" class="entry-shell">
    <div class="entry-top"><span class="brand-mark">e<span>.</span></span><span class="entry-top-text">ELASTIC WEB / CONSOLE</span><span class="local-badge">LOCAL WORKSPACE</span></div>
    <main class="entry-panel">
      <div class="entry-eyebrow"><span class="hairline" /> YOUR WORKSPACE, IN FOCUS</div>
      <h1>让每一次提交<br /><em>有处可去。</em></h1>
      <p class="entry-description">从项目出发，查看真实构建与部署状态。这个本机工作台使用短期作用域令牌；凭据仅留在当前页面内存。</p>
      <form class="entry-form" @submit.prevent="connect">
        <label for="team-id">TEAM ID</label>
        <input id="team-id" v-model.trim="teamInput" type="text" autocomplete="off" placeholder="t_..." required />
        <label for="access-token">访问令牌</label>
        <input id="access-token" v-model="tokenInput" type="password" autocomplete="off" placeholder="仅用于当前会话" required />
        <button class="primary-button entry-submit" type="submit" :disabled="busy">{{ busy ? '正在核对身份…' : '进入项目空间' }} <span aria-hidden="true">↗</span></button>
      </form>
      <p v-if="error" class="form-error" role="alert">{{ error }}</p>
      <p class="entry-footnote">没有公网准入时，此入口只用于隔离开发。项目未部署不会显示可访问的网站。</p>
    </main>
    <div class="entry-side" aria-hidden="true"><span>01 / SOURCE</span><span>02 / PREVIEW</span><span>03 / PRODUCTION</span><span>04 / REWIND</span></div>
  </div>

  <div v-else class="workspace-shell">
    <aside class="sidebar">
      <div class="sidebar-brand"><span class="brand-mark">e<span>.</span></span><span>elastic web<small>DEVELOPER DESK</small></span></div>
      <div class="team-switch"><span class="team-avatar">{{ identity.teamId.slice(2, 4).toUpperCase() }}</span><span class="team-copy"><strong>{{ identity.teamId }}</strong><small>{{ identity.role }} · 当前 Team</small></span><span class="team-caret">⌄</span></div>
      <div class="side-caption">WORKSPACE</div>
      <button class="side-link" :class="{ 'side-link--selected': !selected }" type="button" @click="showProjects()"><span class="side-icon">▦</span> Projects <span class="side-count">{{ projects.length }}</span></button>
      <div class="sidebar-divider" />
      <div class="side-caption side-caption--projects">YOUR PROJECTS</div>
      <nav aria-label="项目列表" class="project-nav">
        <button v-for="project in projects" :key="project.id" class="project-nav-item" :class="{ 'project-nav-item--current': selected?.id === project.id }" type="button" @click="chooseProject(project)"><span class="project-nav-glyph">{{ project.slug.slice(0, 1).toUpperCase() }}</span><span class="project-nav-name">{{ project.name }}</span><span v-if="project.status === 'active'" class="nav-live-dot" title="入口已读回" /></button>
        <p v-if="!projects.length" class="nav-empty">还没有项目</p>
      </nav>
      <button v-if="canCreate" class="sidebar-new" type="button" @click="showProjects(true)">＋ 新建项目</button>
      <button v-if="projectCursor" class="sidebar-more" type="button" :disabled="busy" @click="work(() => loadProjects(true))">加载更多项目 ↓</button>
      <div class="sidebar-bottom"><span class="sidebar-bottom-line" /><span>Built for the next deployment.</span><button class="sidebar-disconnect" type="button" @click="disconnect">断开本次会话 ↗</button></div>
    </aside>

    <div class="workspace-content">
      <header class="topbar"><div class="breadcrumbs"><span>WORKSPACE</span><span class="breadcrumb-slash">/</span><strong>{{ selected?.name || 'Projects' }}</strong></div><div class="topbar-actions"><span class="identity-indicator"><span class="identity-dot" />{{ identity.role }}</span><button class="ghost-button" type="button" :disabled="busy" @click="work(async () => { await loadProjects(); if (selected) await loadProject(selected.id) })">↻ <span>刷新状态</span></button></div></header>

      <main class="main-content">
        <div v-if="error" class="alert alert--error" role="alert"><span>!</span>{{ error }}<button type="button" aria-label="关闭错误" @click="error = ''">×</button></div>
        <div v-if="message" class="alert alert--notice" role="status"><span>✓</span>{{ message }}<button type="button" aria-label="关闭提示" @click="message = ''">×</button></div>

        <template v-if="!selected">
          <div class="page-eyebrow">01 / WORKSPACE</div>
          <div class="page-head"><div><h1>Projects<span class="page-title-dot">.</span></h1><p>你的每个网站，都从一个真实的项目开始。</p></div><button v-if="canCreate" class="primary-button" type="button" @click="createOpen = !createOpen">＋ 新建项目</button></div>
          <div v-if="createOpen" class="inline-form-panel"><span class="panel-number">NEW / 01</span><h2>给网站一个起点</h2><form class="two-field-form" @submit.prevent="createProject"><label>项目名称<input v-model="newName" maxlength="120" placeholder="例如：品牌官网" required /></label><label>项目标识<input v-model="newSlug" pattern="[a-z0-9][a-z0-9-]*" placeholder="brand-site" required /></label><button class="primary-button" type="submit" :disabled="busy">创建项目 →</button></form></div>
          <div v-if="projects.length" class="project-directory"><div class="directory-caption"><span>YOUR PROJECTS</span><span>{{ projects.length }} 个已载入项目</span></div><button v-for="project in projects" :key="project.id" class="directory-row" type="button" @click="chooseProject(project)"><span class="directory-icon">{{ project.slug.slice(0, 1).toUpperCase() }}</span><span class="directory-copy"><strong>{{ project.name }}</strong><small class="mono">{{ project.slug }} / {{ project.id }}</small></span><StatusPill :state="project.status" /><span class="directory-arrow">↗</span></button><button v-if="projectCursor" class="sidebar-more" type="button" :disabled="busy" @click="work(() => loadProjects(true))">加载更多项目 ↓</button></div>
          <div v-else class="empty-projects"><div class="empty-orbit" aria-hidden="true"><span>+</span></div><h2>这里还没有网站</h2><p>创建项目后，你可以关联 Git 仓库，或交给 Agent 上传静态源码。</p><button v-if="canCreate && !createOpen" class="text-button" type="button" @click="createOpen = true">创建第一个项目 ↗</button></div>
        </template>

        <template v-else>
          <div class="page-eyebrow">PROJECT / {{ selected.slug.toUpperCase() }}</div>
          <div class="page-head project-head"><div><div class="project-title-line"><span class="project-title-mark">{{ selected.slug.slice(0, 1).toUpperCase() }}</span><h1>{{ selected.name }}<span class="page-title-dot">.</span></h1></div><div class="project-subline"><span class="mono">{{ selected.slug }}</span><span class="meta-separator">/</span><StatusPill :state="selected.status" /></div></div><button v-if="canDeploy" class="primary-button" type="button" @click="activeTab = 'deployments'">＋ 提交新版本</button></div>
          <nav class="project-tabs" aria-label="项目视图"><button :class="{ 'tab--active': activeTab === 'overview' }" type="button" @click="activeTab = 'overview'">Overview</button><button :class="{ 'tab--active': activeTab === 'deployments' }" type="button" @click="activeTab = 'deployments'">Deployments <span>{{ deployments.length }}</span></button><button :class="{ 'tab--active': activeTab === 'settings' }" type="button" @click="activeTab = 'settings'">Settings</button></nav>
          <p v-if="loadingProject" class="load-note">正在核对数据库与部署字节…</p>
          <div v-else-if="projectProblem" class="state-block state-block--error"><h2>状态暂不可核对</h2><p>{{ projectProblem }}</p><button class="small-button" type="button" @click="loadProject(selected.id)">重试</button></div>

          <template v-else-if="activeTab === 'overview'">
            <div class="overview-grid"><section class="production-panel"><div class="section-heading"><span>PRODUCTION / LIVE TARGET</span><StatusPill :state="production?.state || 'not_deployed'" /></div><div v-if="production" class="production-body"><div class="domain-label">{{ production.desiredHost }}</div><p v-if="production.state === 'active'" class="production-caption">网关已读回当前版本 · generation {{ production.observedGeneration }}</p><p v-else class="production-caption">已提交 generation {{ production.desiredGeneration }}，网关仅确认到 {{ production.observedGeneration }}。此链接暂不能当作已上线地址。</p><a v-if="productionURL" class="outbound-link" :href="productionURL" target="_blank" rel="noopener noreferrer">访问已核实的站点 ↗</a><span v-else class="domain-unverified">未配置或未核验生产域名 · 不提供可点击链接</span></div><div v-else class="production-empty"><div class="empty-rule" /><h2>还没有生产版本。</h2><p>先提交静态源码、访问受保护的 Preview，再选择要发布的版本。</p></div></section><section class="source-panel"><div class="section-heading"><span>SOURCE / CONNECTED REPOSITORY</span><span class="panel-arrow">↗</span></div><template v-if="github"><div class="source-icon">⌘</div><h2>{{ github.fullName || '仓库待核验' }}</h2><StatusPill :state="github.status" /><p>Production branch · <span class="mono">{{ github.defaultBranch || '待查询' }}</span></p></template><template v-else><div class="source-icon source-icon--empty">⌁</div><h2>暂无 Git 来源</h2><p>可以先上传源码；仓库授权需在 Settings 中按真实权限完成。</p></template></section></div>
            <div class="section-toolbar"><div><span class="section-kicker">RECENT ACTIVITY</span><h2>最近部署</h2></div><button class="text-button" type="button" @click="activeTab = 'deployments'">查看全部 ↗</button></div>
            <div v-if="!deployments.length" class="state-block"><div class="empty-glyph">↗</div><h2>还没有部署记录</h2><p>创建项目不等于网站已经运行。提交首个静态源码后，真实记录会出现在这里。</p></div>
            <div v-else class="deployment-list"><DeploymentLine v-for="deployment in deployments.slice(0, 3)" :key="deployment.deploymentId" :deployment="deployment" :current-production="activeProduction === deployment.deploymentId" :rollback-target="isRollbackTarget(deployment)" :can-promote="canPromote" :busy="busy" @preview="openPreview" @promote="promote" /></div>
            <div class="section-toolbar"><div><span class="section-kicker">GIT / SOURCE STATUS</span><h2>Git 提交状态</h2></div><span class="toolbar-count">最近 {{ gitPushes.length }} 条</span></div>
            <div v-if="!gitPushes.length" class="state-block"><h2>尚无 Git 提交记录</h2><p>绑定仓库并完成来源核验后，这里会显示真实构建阶段。手工导入的操作员制品不会冒充 Git 自动部署。</p></div>
            <div v-else class="deployment-list"><div v-for="push in gitPushes" :key="push.deliveryId" class="deployment-line"><div class="deployment-main"><div class="deployment-topline"><span class="mono">{{ push.commitSha.slice(0, 7) }}</span><StatusPill :state="push.status" /></div><p class="deployment-meta"><span class="mono">{{ push.ref }}</span><span>·</span><span>{{ push.origin === 'app_poll' ? 'App 轮询' : push.origin === 'actions_oidc' ? 'Actions OIDC' : '已验签 Webhook' }}</span><span v-if="push.errorCode">· {{ push.errorCode }}</span></p><p class="git-build-boundary">{{ buildState(push) }}<template v-if="push.sourceBytes"> · 已核验源码 {{ push.sourceBytes.toLocaleString() }} B</template><template v-if="push.buildAcceptedAt"> · 验收于 {{ new Date(push.buildAcceptedAt).toLocaleString() }}</template></p></div><span v-if="push.deploymentId" class="mono short-id">{{ push.deploymentId }}</span></div><div v-if="gitPushCursor" class="more-row"><button class="small-button" type="button" :disabled="busy" @click="loadMoreGitPushes">加载更早的 Git 提交 ↓</button></div></div>
            <div class="section-toolbar"><div><span class="section-kicker">CONTROL / AUDIT</span><h2>交付事件</h2></div><span class="toolbar-count">最近 {{ activity.length }} 条</span></div>
            <div v-if="!activity.length" class="state-block"><h2>暂无交付事件</h2><p>有经过核验的源码、构建作业或实际读回后才会记录。</p></div>
            <div v-else class="deployment-list"><div v-for="event in activity" :key="`${event.action}-${event.subjectId}-${event.at}`" class="deployment-line"><div class="deployment-main"><strong>{{ event.action }}</strong><p class="deployment-meta mono">{{ event.subjectId }} · {{ new Date(event.at).toLocaleString() }}</p></div></div></div>
          </template>

          <template v-else-if="activeTab === 'deployments'">
            <div class="section-toolbar deployments-toolbar"><div><span class="section-kicker">VERSION HISTORY</span><h2>每次提交，都有记录。</h2></div><span class="toolbar-count">{{ deployments.length }} 个已载入版本</span></div>
            <div v-if="canDeploy" class="upload-panel"><div><span class="panel-number">NEW DEPLOYMENT / STATIC</span><h3>从源码包创建候选</h3><p>只接收无需构建脚本的普通 HTML。Vite/Next.js 在隔离 Builder 就绪前保持阻断。</p></div><form class="upload-actions" @submit.prevent="deployStatic"><label class="file-select"><input type="file" accept=".gz,.tgz,application/gzip" @change="selectFile" />{{ sourceFile?.name || '选择 tar.gz 源码包' }}</label><button class="primary-button" type="submit" :disabled="busy || !sourceFile">{{ busy ? '正在提交…' : '创建候选 ↗' }}</button></form></div>
            <div v-if="!deployments.length" class="state-block"><div class="empty-glyph">◇</div><h2>还没有部署记录</h2><p>当源码通过实际字节校验，新的候选将出现在这里。</p></div>
            <div v-else class="deployment-list"><DeploymentLine v-for="deployment in deployments" :key="deployment.deploymentId" :deployment="deployment" :current-production="activeProduction === deployment.deploymentId" :rollback-target="isRollbackTarget(deployment)" :can-promote="canPromote" :busy="busy" @preview="openPreview" @promote="promote" /><div v-if="deploymentCursor" class="more-row"><button class="small-button" type="button" :disabled="busy" @click="loadMoreDeployments">加载更早的部署 ↓</button></div></div>
            <p class="page-boundary">部署状态来自控制数据库。仅当 Preview 正文由受信 HTTPS 入口提供后，才能申请生产；已申请不代表全网生效。</p>
          </template>

          <template v-else>
            <div class="section-toolbar settings-heading"><div><span class="section-kicker">CONFIGURATION</span><h2>项目设置</h2></div></div>
            <div class="settings-section"><div class="settings-intro"><span class="setting-index">01</span><div><h3>GitHub Repository</h3><p>来源归属会同时核对 GitHub 用户和 App 的单仓库权限。</p></div></div><div class="settings-body"><template v-if="github"><p class="setting-line"><span>当前仓库</span><strong>{{ github.fullName || '待核验' }}</strong></p><p class="setting-line"><span>状态</span><StatusPill :state="github.status" /></p></template><div v-else class="setting-empty">尚未连接仓库；从 Agent 上传仍可创建静态候选。</div><form v-if="canGitConnect && github?.status !== 'verified'" class="git-form" @submit.prevent="connectGitHub"><p>受控接入入口 · 目前需要预先取得 GitHub App Installation ID 与 Repository ID；仓库选择器仍待开发。</p><div class="git-fields"><label>Installation ID<input v-model="installationID" type="text" inputmode="numeric" placeholder="123456789" required /></label><label>Repository ID<input v-model="repositoryID" type="text" inputmode="numeric" placeholder="987654321" required /></label></div><button class="small-button" type="submit" :disabled="busy">前往 GitHub 授权 ↗</button></form><button class="text-button refresh-binding" type="button" :disabled="busy" @click="loadProject(selected.id)">刷新仓库状态 ↻</button></div></div>
             <div class="settings-section"><div class="settings-intro"><span class="setting-index">02</span><div><h3>资源预留</h3><p>预算与申请来自控制数据库。预留不代表 Node 已运行，也不是 CPU、内存或磁盘的实际用量。</p></div></div><div class="settings-body"><div v-if="teamResources"><p class="setting-line"><span>Team 预算</span><strong>{{ teamResources.budget.cpuMilli }}m / {{ teamResources.budget.memoryMiB }} MiB / {{ teamResources.budget.storageMiB }} MiB</strong></p><p class="setting-line"><span>剩余可预留</span><strong>{{ teamResources.available.cpuMilli }}m / {{ teamResources.available.memoryMiB }} MiB / {{ teamResources.available.storageMiB }} MiB</strong></p></div><p v-else-if="identity.role !== 'agent'" class="setting-empty">尚未设置 Team 预算，无法为项目申请资源。</p><p v-if="projectResources" class="setting-line"><span>项目状态</span><strong>{{ projectResources.state === 'unallocated' ? '未预留' : '已预留 · 尚未配给运行时' }}（v{{ projectResources.version }}）</strong></p><form v-if="identity.role === 'owner' && projectResources && teamResources" class="git-form" @submit.prevent="reserveResources"><p>Owner 可在 Team 预算内调整预留，三项全部填 0 可释放。任何申请都不会在此步骤启动容器。</p><div class="git-fields"><label>CPU（毫核）<input v-model.number="cpuMilli" type="number" min="0" max="100000" step="1" required /></label><label>内存（MiB）<input v-model.number="memoryMiB" type="number" min="0" max="1048576" step="1" required /></label><label>存储（MiB）<input v-model.number="storageMiB" type="number" min="0" max="1048576" step="1" required /></label></div><button class="small-button" type="submit" :disabled="busy">保存资源预留 ↗</button></form></div></div>
             <div class="settings-section"><div class="settings-intro"><span class="setting-index">03</span><div><h3>已核验的交付数据</h3><p>统计项目内全部已核验 Git 快照；源码归档字节不等于磁盘、CPU 或请求用量。</p></div></div><div class="settings-body"><p class="setting-line"><span>已核验源码</span><strong>{{ knownGitSourceBytes.toLocaleString() }} B（{{ usage?.verifiedSources || 0 }} 份）</strong></p><p class="setting-line"><span>已签收隔离制品</span><strong>{{ verifiedBuilds }} 次</strong></p><p class="setting-line"><span>请求 / CPU / 持久存储实际用量</span><span class="unavailable">未接计量源，不提供估算值</span></p></div></div>
             <div class="settings-section"><div class="settings-intro"><span class="setting-index">04</span><div><h3>运行边界</h3><p>只展示已核实的配置，不提供任何模拟的资源用量。</p></div></div><div class="settings-body"><p class="setting-line"><span>Team 空间</span><span class="mono">{{ identity.teamId }}</span></p><p class="setting-line"><span>Project ID</span><span class="mono">{{ selected.id }}</span></p><p class="setting-line"><span>当前能力</span><span>静态文件 · 单区域受控验证</span></p><p class="setting-line"><span>隔离构建/动态运行</span><span class="unavailable">Vite / Node 尚未通过平台自动构建及运行准入</span></p></div></div>
          </template>
        </template>
      </main>
      <footer class="workspace-footer"><span>ELASTIC WEB <i>·</i> PROJECT-FIRST DELIVERY</span><span>本机候选 ≠ 公网 SaaS 准入</span></footer>
    </div>
  </div>
</template>
