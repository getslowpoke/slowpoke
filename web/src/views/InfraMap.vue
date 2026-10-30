<script setup>
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { api } from '../lib/api.js'
import { useGet } from '../lib/useLoad.js'
import { t } from '../lib/i18n.js'
import { num, pct, bytes, ago } from '../lib/format.js'
import Sparkline from '../components/Sparkline.vue'
import State from '../components/State.vue'
import PageHeader from '../components/PageHeader.vue'
import Empty from '../components/Empty.vue'

const props = defineProps({ project: Object })
const { data, error, loading, reload } = useGet(() => `/api/projects/${props.project.slug}/topology`, { every: 30000 })
const hovered = ref(null)
const installFor = ref(null)
const selected = ref(null)
const detail = ref(null)
const detailLoading = ref(false)
const renaming = ref(null)
const renameValue = ref('')
const renameInput = ref(null)
// Where the team dragged a node, this session. The server sends the saved ones on every node.
const svgEl = ref(null)
const places = ref({})
const dragging = ref(null)
const justDragged = ref(false)
const placeOf = (n) => places.value[n.id] || (n.x != null && n.y != null ? { x: n.x, y: n.y } : null)
const arranged = computed(() => (data.value?.nodes || []).some((n) => placeOf(n)))

// Client pixels to map coordinates: the SVG scales with the card, so ask the SVG itself.
function toMap(evt) {
  const ctm = svgEl.value?.getScreenCTM()
  if (!ctm) return { x: 0, y: 0 }
  const p = new DOMPoint(evt.clientX, evt.clientY).matrixTransform(ctm.inverse())
  return { x: p.x, y: p.y }
}
function startDrag(evt, p) {
  if (evt.button !== 0 || renaming.value === p.node.id || evt.target?.tagName === 'INPUT') return
  const m = toMap(evt)
  dragging.value = { id: p.node.id, dx: m.x - p.x, dy: m.y - p.y, from: m, moved: false }
  window.addEventListener('pointermove', onDrag)
  window.addEventListener('pointerup', endDrag, { once: true })
}
function onDrag(evt) {
  const d = dragging.value
  if (!d) return
  const m = toMap(evt)
  // A few pixels are a click, not a drag: the box must still be clickable.
  if (!d.moved && Math.abs(m.x - d.from.x) < 4 && Math.abs(m.y - d.from.y) < 4) return
  d.moved = true
  places.value = {
    ...places.value,
    [d.id]: { x: Math.round(Math.min(Math.max(m.x - d.dx, 0), W - BOX_W)), y: Math.round(Math.max(m.y - d.dy, 0)) },
  }
}
async function endDrag() {
  window.removeEventListener('pointermove', onDrag)
  const d = dragging.value
  dragging.value = null
  if (!d?.moved) return
  justDragged.value = true
  setTimeout(() => (justDragged.value = false), 0)
  const place = places.value[d.id]
  await api.put(`/api/projects/${props.project.slug}/topology/places`, { node_id: d.id, x: place.x, y: place.y })
}
async function resetPlaces() {
  places.value = {}
  await api.del(`/api/projects/${props.project.slug}/topology/places`)
  reload()
}
onBeforeUnmount(() => window.removeEventListener('pointermove', onDrag))

async function select(n) {
  selected.value = n
  detail.value = null
  if (!n.host_id) return
  detailLoading.value = true
  try {
    detail.value = await api.get(`/api/projects/${props.project.slug}/hosts/${n.host_id}?window=1h`)
  } finally {
    detailLoading.value = false
  }
}
const last = computed(() => detail.value?.series?.at(-1) || {})
const nodeName = (id) => nodesByID.value[id]?.name || id
const nodesByID = computed(() => Object.fromEntries((data.value?.nodes || []).map((n) => [n.id, n])))
const selectedLinks = computed(() => {
  if (!selected.value) return { out: [], in: [] }
  const edges = data.value?.edges || []
  const ids = new Set([selected.value.id, ...(data.value?.nodes || []).filter((n) => n.parent === selected.value.id).map((n) => n.id)])
  return { out: edges.filter((e) => ids.has(e.from)), in: edges.filter((e) => ids.has(e.to)) }
})

// Renaming: a double click on a machine or a service opens the field.
async function startRename(node) {
  renaming.value = node.id
  renameValue.value = node.name
  await nextTick()
  renameInput.value?.[0]?.focus?.() ?? renameInput.value?.focus?.()
}
async function saveRename(node) {
  const label = renameValue.value.trim() === node.default_name ? '' : renameValue.value
  renaming.value = null
  await api.put(`/api/projects/${props.project.slug}/topology/labels`, { node_id: node.id, label })
  reload()
}

const icons = { web: '🌐', app: '⚙️', database: '🛢️', cache: '⚡', search: '🔎', queue: '📬', realtime: '📡', mail: '✉️', external: '☁️', clients: '👥', metrics: '∿', cdn: '🛡️', service: '◇' }
const colors = { web: '#8ab4ff', app: '#b69cff', database: '#ff8fa6', cache: '#f5c76a', search: '#7ee0a1', queue: '#ffb37a', external: '#7c7c89', clients: '#7c7c89', realtime: '#6ee7e7', cdn: '#ffb37a' }
const roleOf = (n) => (n.roles || [])[0] || (n.kind === 'cdn' ? 'cdn' : 'service')
const opened = ref(new Set())
const isOpen = (id) => opened.value.has(id)
function toggleOpen(id) {
  const next = new Set(opened.value)
  next.has(id) ? next.delete(id) : next.add(id)
  opened.value = next
}
const boxes = computed(() => Object.keys(layout.value.pos))
const allOpen = computed(() => boxes.value.length > 0 && boxes.value.every((id) => isOpen(id)))
function openAll() {
  opened.value = allOpen.value ? new Set() : new Set(boxes.value)
}

const W = 1320
const BOX_W = 260
// Closed, a machine says its name, whether it is up and two numbers. Open, it shows what runs
// inside it. A map of twenty machines has to be readable before it is complete.
const HEAD_H = 104
const HEAD_SHUT = 60
const ROW_H = 32
const GAP_Y = 44
const GAP_ROW = 6

// Columns: who calls, what sits in front, the machines, what they call.
function column(n) {
  if (n.id === 'internet:in' || n.id.startsWith('clients:')) return 0
  if (n.kind === 'cdn') return 1
  if (n.id === 'internet:out' || n.id.startsWith('external:')) return 4
  const roles = n.roles || []
  const onlyData = roles.length > 0 && roles.every((r) => ['database', 'cache', 'search', 'queue'].includes(r))
  return onlyData ? 3 : 2
}

const layout = computed(() => {
  const nodes = data.value?.nodes || []
  const hosts = nodes.filter((n) => n.kind !== 'service' || !n.parent)
  const children = {}
  for (const n of nodes) {
    if (n.parent) (children[n.parent] ||= []).push(n)
  }
  for (const list of Object.values(children)) {
    list.sort((a, b) => (a.local_only === b.local_only ? (a.port || 0) - (b.port || 0) : a.local_only ? 1 : -1))
  }
  const headOf = (n) => (isOpen(n.id) ? HEAD_H : HEAD_SHUT)
  const heightOf = (n) => headOf(n) + (isOpen(n.id) && children[n.id]?.length ? children[n.id].length * (ROW_H + GAP_ROW) + 8 : 0)

  // Machines the team placed by hand are out of the automatic flow.
  const placed = hosts.filter((n) => placeOf(n))
  const flowing = hosts.filter((n) => !placeOf(n))
  const cols = [[], [], [], [], []]
  for (const n of flowing) cols[column(n)].push(n)
  const used = cols.map((c, i) => (c.length ? i : -1)).filter((i) => i >= 0)
  const x = {}
  used.forEach((ci, idx) => (x[ci] = used.length === 1 ? (W - BOX_W) / 2 : 24 + (idx * (W - BOX_W - 48)) / (used.length - 1)))
  const colHeight = cols.map((c) => c.reduce((sum, n) => sum + heightOf(n) + GAP_Y, -GAP_Y))
  const height = Math.max(380, ...colHeight.map((h) => h + 56), ...placed.map((n) => placeOf(n).y + heightOf(n) + 40))

  const pos = {}
  const anchors = {}
  const put = (n, x, y) => {
    const h = heightOf(n)
    const head = headOf(n)
    pos[n.id] = { x, y, w: BOX_W, h, head, open: isOpen(n.id), node: n, children: children[n.id] || [] }
    anchors[n.id] = { left: [x, y + head / 2], right: [x + BOX_W, y + head / 2], inner: [x + 14, y + head - 6] }
    ;(children[n.id] || []).forEach((child, i) => {
      // A closed machine answers for the services inside it: the arrows land on the box.
      if (!isOpen(n.id)) {
        anchors[child.id] = anchors[n.id]
        return
      }
      const cy = y + head + 8 + i * (ROW_H + GAP_ROW) + ROW_H / 2
      anchors[child.id] = { left: [x + 8, cy], right: [x + BOX_W - 8, cy], inner: [x + 8, cy] }
    })
    return h
  }
  for (const n of placed) put(n, placeOf(n).x, placeOf(n).y)
  cols.forEach((c, ci) => {
    let y = (height - colHeight[ci]) / 2
    for (const n of c) {
      y += put(n, x[ci], y) + GAP_Y
    }
  })
  return { pos, anchors, height, children }
})

const edges = computed(() => {
  const { anchors } = layout.value
  const list = (data.value?.edges || []).filter((e) => anchors[e.from] && anchors[e.to])
  const maxConn = Math.max(1, ...list.map((e) => e.connections))
  return list.map((e, i) => {
    const a = anchors[e.from]
    const b = anchors[e.to]
    const to = nodesByID.value[e.to] || {}
    // Two services of the same machine: the line stays inside the box, in the left margin.
    const internal = to.parent && (to.parent === e.from || nodesByID.value[e.from]?.parent === to.parent)
    let x1, y1, x2, y2, d
    if (internal) {
      ;[x1, y1] = a.inner
      ;[x2, y2] = b.inner
      const bend = Math.min(26, 10 + Math.abs(y2 - y1) * 0.25)
      d = `M${x1},${y1} C${x1 - bend},${y1 + 8} ${x2 - bend},${y2 - 8} ${x2},${y2}`
    } else {
      const forward = b.left[0] > a.right[0] - 40
      ;[x1, y1] = forward ? a.right : a.left
      ;[x2, y2] = forward ? b.left : b.right
      const dx = Math.max(50, Math.abs(x2 - x1) * 0.4)
      d = forward
        ? `M${x1},${y1} C${x1 + dx},${y1} ${x2 - dx},${y2} ${x2},${y2}`
        : `M${x1},${y1} C${x1 - dx},${y1} ${x2 + dx},${y2} ${x2},${y2}`
    }
    const role = roleOf(nodesByID.value[e.to] || {})
    return {
      ...e, i, d, internal, color: colors[role] || '#7c7c89',
      width: 1.2 + (e.connections / maxConn) * 3,
      speed: Math.max(0.6, 3 - (e.connections / maxConn) * 2.4),
      mx: (x1 + x2) / 2, my: (y1 + y2) / 2,
    }
  })
})

// A machine is highlighted with its services: hovering the box lights up all its links.
function familiy(id) {
  const node = nodesByID.value[id]
  if (!node) return new Set([id])
  const ids = new Set([id])
  if (node.parent) ids.add(node.parent)
  for (const n of data.value?.nodes || []) if (n.parent === id) ids.add(n.id)
  return ids
}
function active(e) {
  if (!hovered.value) return true
  const ids = familiy(hovered.value)
  return ids.has(e.from) || ids.has(e.to)
}

const stats = computed(() => {
  const nodes = data.value?.nodes || []
  return {
    hosts: nodes.filter((n) => n.kind === 'host').length,
    online: nodes.filter((n) => n.kind === 'host' && n.online).length,
    services: nodes.filter((n) => n.kind === 'service' && n.parent).length,
    unmonitored: nodes.filter((n) => n.kind === 'service' && !n.parent).length,
    links: (data.value?.edges || []).length,
  }
})
const installIP = computed(() => installFor.value?.id.split(':')[1] || '')
</script>

<template>
  <State :loading="loading && !data" :error="error" @retry="reload">
    <PageHeader :title="t('Infrastructure')" :subtitle="t('Machines, the services inside them and who talks to whom, from the live connections seen by every agent.')">
      <span class="chip"><span class="h-1.5 w-1.5 rounded-full bg-good" />{{ stats.online }}/{{ stats.hosts }} {{ t('online') }}</span>
      <span class="chip">▦ {{ stats.services }} {{ t('services') }}</span>
      <span class="chip">⇄ {{ stats.links }} {{ t('links') }}</span>
      <span v-if="stats.unmonitored" class="chip !border-warn/30 !text-warn">◇ {{ stats.unmonitored }} {{ t('without agent') }}</span>
      <button class="btn-ghost !py-1 text-xs" @click="openAll">{{ allOpen ? t('Close them all') : t('Open them all') }}</button>
      <button v-if="arranged" class="btn-ghost !py-1 text-xs" @click="resetPlaces">{{ t('Tidy up automatically') }}</button>
    </PageHeader>

    <Empty v-if="!data.nodes.length" icon="⌘" :title="t('No data yet')" :text="t('Install an agent on your servers and data shows up within a minute.')" />

    <p v-if="data.nodes.length" class="mb-2 text-xs text-ink-500">{{ t('Click a machine for everything about it, ▸ to see what runs inside it, double click a name to change it, drag it to arrange the map: where you put it is saved.') }}</p>
    <div v-if="data.nodes.length" class="card overflow-x-auto p-2">
      <svg ref="svgEl" :viewBox="`0 0 ${W} ${layout.height}`" class="min-w-[1000px]" :style="{ height: layout.height * 0.92 + 'px', width: '100%' }">
        <defs>
          <pattern id="grid" width="28" height="28" patternUnits="userSpaceOnUse">
            <circle cx="1" cy="1" r="1" fill="rgba(255,255,255,0.05)" />
          </pattern>
        </defs>
        <rect :width="W" :height="layout.height" fill="url(#grid)" />

        <g v-for="e in edges.filter((x) => !x.internal)" :key="e.from + e.to + e.service" :style="{ opacity: active(e) ? 1 : 0.1, transition: 'opacity .2s' }">
          <path :d="e.d" fill="none" :stroke="e.color" :stroke-width="e.width + 3" stroke-opacity="0.06" />
          <path :d="e.d" fill="none" :stroke="e.color" :stroke-width="e.width" stroke-opacity="0.35" />
          <path v-if="!e.stale" :d="e.d" fill="none" :stroke="e.color" :stroke-width="e.width" stroke-linecap="round" stroke-dasharray="2 14" class="flow" :style="{ animationDuration: e.speed + 's' }" />
          <path v-else :d="e.d" fill="none" stroke="#52525e" :stroke-width="e.width" stroke-dasharray="4 6" />
          <foreignObject v-if="hovered && active(e)" :x="e.mx - 80" :y="e.my - 12" width="160" height="24">
            <div class="flex justify-center">
              <span class="whitespace-nowrap rounded-full border border-ink-600 bg-ink-850 px-2 py-0.5 font-mono text-[10px] text-ink-300">
                {{ e.service }} · {{ num(e.connections) }}<template v-if="e.stale"> · {{ t('not updated') }}</template>
              </span>
            </div>
          </foreignObject>
        </g>

        <foreignObject v-for="(p, id) in layout.pos" :key="id" :x="p.x" :y="p.y" :width="p.w" :height="p.h">
          <div
            class="h-full touch-none rounded-2xl border transition"
            :class="[
              p.node.kind === 'host' ? 'cursor-move bg-ink-900' : 'bg-ink-900/60',
              p.node.kind === 'service' && !p.node.parent ? 'cursor-move border-dashed border-warn/40 opacity-60 hover:opacity-100' : 'border-ink-700',
              hovered === id ? '!border-poke-500/60 shadow-[0_0_30px_-8px_rgb(255_111_142/0.45)]' : '',
              dragging?.id === id ? 'opacity-90 shadow-[0_0_40px_-6px_rgb(255_111_142/0.5)]' : '',
            ]"
            @mouseenter="hovered = id" @mouseleave="hovered = null" @pointerdown="startDrag($event, p)"
          >
            <div class="p-3" :data-node="p.node.kind" @click="justDragged ? null : (p.node.kind === 'host' ? select(p.node) : (p.node.parent ? null : (installFor = p.node)))">
              <div class="relative flex items-center gap-2">
                <span v-if="p.node.kind === 'service' && !p.node.parent" :title="t('No agent: this service is not monitored')"
                  class="absolute -right-4 -top-4 grid h-5 w-5 place-items-center rounded-full border border-warn/60 bg-ink-950 text-[11px] font-bold text-warn">!</span>
                <span class="grid h-8 w-8 shrink-0 place-items-center rounded-xl text-base" :style="{ background: (colors[roleOf(p.node)] || '#7c7c89') + '22' }">
                  {{ icons[roleOf(p.node)] || '▦' }}
                </span>
                <div class="min-w-0 flex-1">
                  <input v-if="renaming === p.node.id" ref="renameInput" v-model="renameValue" class="input !h-7 !px-2 !py-0 text-[13px]"
                    @click.stop @keyup.enter="saveRename(p.node)" @keyup.esc="renaming = null" @blur="saveRename(p.node)" />
                  <div v-else class="truncate text-[13px] font-medium text-ink-100" :title="t('Double click to rename')" @dblclick.stop="startRename(p.node)">
                    {{ p.node.name }}
                    <span v-if="p.node.default_name" class="text-ink-600">·</span>
                  </div>
                  <!-- Closed: the two numbers that say how it is doing. Open: where it lives. -->
                  <div v-if="p.open" class="truncate font-mono text-[10px] text-ink-500">{{ p.node.addresses?.[0] || p.node.default_name || (p.node.kind === 'service' ? t('not monitored') : '') }}</div>
                  <div v-else class="truncate text-[10px] text-ink-500">
                    <template v-if="p.node.cpu !== undefined && p.node.cpu !== null">
                      <span class="num">CPU {{ pct(p.node.cpu, 0) }}</span> · <span class="num">RAM {{ pct(p.node.mem_percent, 0) }}</span>
                    </template>
                    <template v-else-if="p.node.kind === 'service' && !p.node.parent">{{ t('not monitored') }}<template v-if="p.node.port"> · :{{ p.node.port }}</template></template>
                    <template v-else>{{ p.node.addresses?.[0] || p.node.default_name || '' }}</template>
                  </div>
                </div>
                <span v-if="p.children.length" class="chip shrink-0 !px-1.5 !py-0 !text-[10px]" :title="t('services inside')">▦ {{ p.children.length }}</span>
                <span v-if="p.node.kind === 'host'" class="h-2 w-2 shrink-0 rounded-full" :class="p.node.online ? 'bg-good shadow-[0_0_8px_var(--color-good)]' : 'bg-ink-500'" />
                <button class="shrink-0 rounded-md px-1 text-[11px] text-ink-500 transition hover:text-ink-200"
                  :title="p.open ? t('Close') : t('What is inside')" @click.stop="toggleOpen(id)">{{ p.open ? '▾' : '▸' }}</button>
              </div>
              <div v-if="p.open && p.node.cpu !== undefined && p.node.cpu !== null" class="mt-2 grid grid-cols-2 gap-2 text-[10px] text-ink-400">
                <div>
                  <div class="flex justify-between"><span>CPU</span><span class="num text-ink-300">{{ pct(p.node.cpu, 0) }}</span></div>
                  <div class="mt-0.5 h-1 overflow-hidden rounded-full bg-ink-800"><div class="h-full rounded-full bg-poke-500" :style="{ width: p.node.cpu + '%' }" /></div>
                </div>
                <div>
                  <div class="flex justify-between"><span>RAM</span><span class="num text-ink-300">{{ pct(p.node.mem_percent, 0) }}</span></div>
                  <div class="mt-0.5 h-1 overflow-hidden rounded-full bg-ink-800"><div class="h-full rounded-full bg-info" :style="{ width: (p.node.mem_percent || 0) + '%' }" /></div>
                </div>
              </div>
              <button v-else-if="p.open && p.node.kind === 'service' && !p.node.parent" class="mt-2 rounded-lg border border-warn/40 px-2 py-0.5 text-[10px] text-warn hover:bg-warn/10" @click.stop="installFor = p.node">
                + {{ t('How to install the agent') }}
              </button>
            </div>

            <!-- The services running inside this machine. -->
            <div v-if="p.open && p.children.length" class="px-2 pb-2">
              <div v-for="c in p.children" :key="c.id" class="mb-1.5 flex items-center gap-2 rounded-xl border px-2 py-1.5 transition"
                :class="hovered === c.id ? 'border-poke-500/50 bg-ink-850' : 'border-ink-800 bg-ink-850/40'"
                @mouseenter.stop="hovered = c.id" @mouseleave.stop="hovered = id">
                <span class="grid h-5 w-5 shrink-0 place-items-center rounded-md text-[11px]" :style="{ background: (colors[roleOf(c)] || '#7c7c89') + '22' }">{{ icons[roleOf(c)] || '▦' }}</span>
                <input v-if="renaming === c.id" ref="renameInput" v-model="renameValue" class="input !h-6 !px-2 !py-0 text-[11px]"
                  @click.stop @keyup.enter="saveRename(c)" @keyup.esc="renaming = null" @blur="saveRename(c)" />
                <template v-else>
                  <span class="min-w-0 flex-1 truncate text-[11.5px] text-ink-200" :title="t('Double click to rename')" @dblclick.stop="startRename(c)">{{ c.name }}</span>
                  <span v-if="c.local_only" class="shrink-0 text-[9.5px] text-ink-600">{{ t('local') }}</span>
                  <span v-if="c.ports?.length" class="shrink-0 font-mono text-[10px] text-ink-500">{{ c.ports.map((x) => ':' + x).join(' ') }}</span>
                </template>
              </div>
            </div>
          </div>
        </foreignObject>

        <!-- Links between services of the same machine: drawn over the box, inside its margin. -->
        <g v-for="e in edges.filter((x) => x.internal)" :key="'in' + e.from + e.to + e.service" :style="{ opacity: active(e) ? 0.9 : 0.12, transition: 'opacity .2s' }">
          <path :d="e.d" fill="none" :stroke="e.color" stroke-width="1.6" stroke-opacity="0.5" />
          <path v-if="!e.stale" :d="e.d" fill="none" :stroke="e.color" stroke-width="1.6" stroke-linecap="round" stroke-dasharray="2 10" class="flow" :style="{ animationDuration: e.speed + 's' }" />
        </g>
      </svg>
    </div>

    <Teleport to="body">
    <transition name="fade">
      <aside v-if="selected" class="fixed inset-y-0 right-0 z-40 w-full max-w-md overflow-y-auto border-l border-ink-700 bg-ink-900/97 p-6 shadow-2xl backdrop-blur">
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <span class="h-2.5 w-2.5 rounded-full" :class="selected.online ? 'bg-good shadow-[0_0_10px_var(--color-good)]' : 'bg-ink-500'" />
              <h2 class="truncate text-xl font-semibold">{{ selected.name }}</h2>
            </div>
            <div class="mt-1 font-mono text-xs text-ink-500">{{ selected.addresses?.join(' · ') }}</div>
          </div>
          <button class="btn-ghost !px-2 !py-1 text-xs" @click="selected = null">✕</button>
        </div>
        <div class="mt-3 flex flex-wrap gap-1">
          <span v-for="r in selected.roles" :key="r" class="rounded-md px-1.5 py-px text-[11px]" :style="{ color: colors[r] || '#a6a6b2', background: (colors[r] || '#7c7c89') + '18' }">{{ r }}</span>
          <span v-if="selected.cpus" class="chip">{{ selected.cpus }} vCPU</span>
          <span v-if="detail" class="chip">{{ detail.host.os }} · agent {{ detail.host.version }}</span>
          <span v-if="detail && !detail.host.online" class="chip">{{ t('last seen') }} {{ ago(detail.host.last_seen_at) }}</span>
        </div>

        <div v-if="detailLoading" class="mt-6 h-40 animate-pulse rounded-2xl bg-ink-850" />
        <template v-else-if="detail">
          <div class="mt-5 grid grid-cols-2 gap-3">
            <div v-for="m in [
              { l: 'CPU', v: last.cpu, series: detail.series.map((x) => x.cpu), c: '#ff6f8e' },
              { l: t('Memory'), v: last.mem_percent, series: detail.series.map((x) => x.mem_percent), c: '#8ab4ff' },
              { l: t('Swap'), v: last.swap_percent, series: detail.series.map((x) => x.swap_percent), c: '#f5c76a' },
              { l: t('Disk'), v: last.disk_percent, series: detail.series.map((x) => x.disk_percent), c: '#7ee0a1' },
            ]" :key="m.l" class="rounded-xl border border-ink-800 bg-ink-850/50 p-3">
              <div class="flex justify-between text-xs text-ink-400"><span>{{ m.l }}</span><span class="num text-ink-100">{{ pct(m.v, 0) }}</span></div>
              <Sparkline class="mt-2" :values="m.series" :color="m.c" :width="160" :height="28" />
            </div>
          </div>
          <div class="mt-3 grid grid-cols-3 gap-2 text-xs">
            <div class="rounded-xl bg-ink-850/50 p-2.5"><div class="text-ink-500">steal</div><div class="num text-ink-200" :class="last.steal > 5 ? '!text-warn' : ''">{{ pct(last.steal, 1) }}</div></div>
            <div class="rounded-xl bg-ink-850/50 p-2.5"><div class="text-ink-500">rx</div><div class="num text-ink-200">{{ bytes(last.net_rx_bps) }}/s</div></div>
            <div class="rounded-xl bg-ink-850/50 p-2.5"><div class="text-ink-500">tx</div><div class="num text-ink-200">{{ bytes(last.net_tx_bps) }}/s</div></div>
          </div>
          <div v-if="detail.oom_kills" class="mt-3 rounded-xl border border-bad/30 bg-bad/10 p-3 text-sm text-bad">💥 {{ num(detail.oom_kills) }} {{ t('Out of memory kills') }}</div>

          <div class="label mt-6">{{ t('Services') }}</div>
          <div class="mt-2 flex flex-wrap gap-1">
            <span v-for="c in layout.children[selected.id] || []" :key="c.id" class="chip">{{ c.name }}<span v-if="c.port" class="font-mono text-ink-500"> :{{ c.port }}</span></span>
          </div>
        </template>

        <div class="label mt-6">{{ t('Talks to') }}</div>
        <div class="mt-2 space-y-1.5">
          <div v-for="e in selectedLinks.out" :key="'o' + e.from + e.to + e.service" class="flex items-center justify-between gap-3 rounded-lg bg-ink-850/50 px-3 py-2 text-sm">
            <span class="truncate">→ {{ nodeName(e.to) }}</span><span class="shrink-0 font-mono text-xs text-ink-400">{{ e.service }} · {{ num(e.connections) }}</span>
          </div>
          <p v-if="!selectedLinks.out.length" class="text-xs text-ink-500">—</p>
        </div>
        <div class="label mt-4">{{ t('Receives from') }}</div>
        <div class="mt-2 space-y-1.5">
          <div v-for="e in selectedLinks.in" :key="'i' + e.from + e.to + e.service" class="flex items-center justify-between gap-3 rounded-lg bg-ink-850/50 px-3 py-2 text-sm">
            <span class="truncate">← {{ nodeName(e.from) }}</span><span class="shrink-0 font-mono text-xs text-ink-400">{{ e.service }} · {{ num(e.connections) }}</span>
          </div>
          <p v-if="!selectedLinks.in.length" class="text-xs text-ink-500">—</p>
        </div>

        <div class="label mt-6">{{ t('Source') }}</div>
        <div class="mt-2 flex flex-wrap gap-1"><span v-for="src in selected.sources" :key="src" class="chip font-mono">{{ src }}</span></div>

        <router-link v-if="selected.host_id" :to="`/projects/${project.slug}/hosts/${selected.host_id}`" class="btn-primary mt-6 w-full">{{ t('Full details') }} →</router-link>
      </aside>
    </transition>
    </Teleport>

    <Teleport to="body">
    <div v-if="installFor" class="fixed inset-0 z-50 grid place-items-center bg-ink-950/75 p-4 backdrop-blur-sm" @click.self="installFor = null">
      <div class="card w-full max-w-xl p-6">
        <div class="flex items-start justify-between gap-4">
          <div>
            <div class="label">{{ t('Not monitored') }}</div>
            <h2 class="mt-1 text-xl font-semibold">{{ installFor.name }}</h2>
          </div>
          <button class="btn-ghost !px-2 !py-1 text-xs" @click="installFor = null">✕</button>
        </div>
        <p class="mt-3 text-sm text-ink-300">{{ t('Your machines talk to this service, but no agent runs on it: its slow commands, memory and CPU stay invisible.') }}</p>
        <ol class="mt-4 space-y-3 text-sm">
          <li><span class="text-poke-400">1.</span> {{ t('Connect to the machine') }} <code class="chip font-mono">ssh {{ installIP }}</code></li>
          <li><span class="text-poke-400">2.</span> {{ t('Install the agent with the project token and add the host source') }}</li>
          <li><span class="text-poke-400">3.</span> {{ t('The machine appears on this map within a minute.') }}</li>
        </ol>
        <div class="mt-5 flex flex-wrap gap-2">
          <router-link :to="`/projects/${project.slug}/hosts`" class="btn-primary">{{ t('Open the installation guide') }} →</router-link>
          <button class="btn-ghost" @click="installFor = null">{{ t('Close') }}</button>
        </div>
      </div>
    </div>
    </Teleport>
  </State>
</template>

<style scoped>
.flow {
  animation: flow linear infinite;
}
@keyframes flow {
  to {
    stroke-dashoffset: -32;
  }
}
</style>
