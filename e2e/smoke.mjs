// Visits every page of the UI as a logged-in user: fails on console errors,
// failed requests or pages that render (almost) nothing. Saves screenshots.
import puppeteer from 'puppeteer-core'
import fs from 'node:fs'

const base = process.env.BASE || 'http://host.docker.internal:8088'
const slug = process.env.SLUG || 'demo-shop'
const out = process.env.OUT || '/out'
fs.mkdirSync(out, { recursive: true })

const browser = await puppeteer.launch({ executablePath: process.env.CHROME_PATH, args: ['--no-sandbox', '--disable-gpu'] })
const page = await browser.newPage()
await page.setViewport({ width: 1440, height: 1000, deviceScaleFactor: 1 })
const problems = []
let current = ''
page.on('console', (m) => { if ((m.type() === 'error' || m.type() === 'warning') && !m.text().includes('401')) problems.push(`${current} console.${m.type()}: ${m.text()}`) })
page.on('pageerror', (e) => problems.push(`${current} pageerror: ${e.message}`))
page.on('requestfailed', (r) => problems.push(`${current} requestfailed: ${r.url()} ${r.failure()?.errorText}`))
page.on('response', (r) => { if (r.status() >= 400 && !r.url().endsWith('/api/me')) problems.push(`${current} HTTP ${r.status()}: ${r.url()}`) })

await page.goto(`${base}/login`, { waitUntil: 'networkidle0' })
await page.type('input[type=email]', process.env.EMAIL || 'demo@slowpoke.local')
await page.type('input[type=password]', process.env.PASSWORD || 'slowpoke-demo-2026')
await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle0' }), page.click('button')])

const hash = process.env.HASH || await page.evaluate(async (s) => (await (await fetch(`/api/projects/${s}/slow?window=7d`)).json()).queries[0]?.hash, slug)
// A project with the debt under control has no open mission: the detail page is then visited on a
// closed one, and skipped altogether when there is none. Asking for /missions/undefined proved
// nothing except that the test was written on a day with work to do.
const missionId = await page.evaluate(async (s) => {
  const m = await (await fetch(`/api/projects/${s}/missions`)).json()
  return (m.open || [])[0]?.id ?? (m.closed || [])[0]?.id ?? null
}, slug)
const host = process.env.HOST || await page.evaluate(async (s) => (await (await fetch(`/api/projects/${s}/hosts`)).json()).hosts[0]?.id, slug)
const pages = [
  ['projects', '/'],
  ['overview', `/projects/${slug}`],
  ['map', `/projects/${slug}/map`],
  ['slow', `/projects/${slug}/slow`],
  ['query', `/projects/${slug}/queries/${hash}`],
  ['endpoint', `/projects/${slug}/endpoint?source=nginx:app&endpoint=${encodeURIComponent('GET /search')}`],
  ['missions', `/projects/${slug}/missions`],
  ...(missionId ? [['mission', `/projects/${slug}/missions/${missionId}`]] : []),
  ['deploys', `/projects/${slug}/deploys`],
  ['hosts', `/projects/${slug}/hosts`],
  ['host', `/projects/${slug}/hosts/${host}`],
  ['metrics', `/projects/${slug}/metrics`],
  ['jobs', `/projects/${slug}/jobs`],
  ['settings', `/projects/${slug}/settings`],
  ['report', `/projects/${slug}/report`],
  ['tv', `/tv/${slug}`],
]
// The welcome tour opens on the first visit of a browser, once: it must open, close with Escape,
// and then stay closed on every page after (or it would cover all the screenshots below).
current = 'welcome tour'
await page.goto(`${base}/projects/${slug}`, { waitUntil: 'networkidle0' })
if (!(await page.$('[role=dialog]'))) problems.push('welcome tour: did not open on the first visit')
await page.keyboard.press('Escape')
await page.goto(`${base}/projects/${slug}/slow`, { waitUntil: 'networkidle0' })
if (await page.$('[role=dialog]')) problems.push('welcome tour: opened again after being closed')

const results = []
for (const [vw, vh] of [[1440, 1000], [390, 844]]) {
await page.setViewport({ width: vw, height: vh, deviceScaleFactor: 1 })
for (const [name, path] of pages) {
  current = `[${name} @${vw}]`
  await page.goto(base + path, { waitUntil: 'networkidle0' })
  await new Promise((r) => setTimeout(r, 900))
  const text = await page.evaluate(() => (document.querySelector('main') || document.body).innerText.trim().length)
  // Content hidden by a stuck transition: visible text with (almost) zero opacity.
  const hidden = await page.evaluate(() => {
    const root = document.querySelector('main') || document.querySelector('#app')
    let el = root.firstElementChild
    let min = 1
    for (let depth = 0; el && depth < 4; depth++, el = el.firstElementChild) min = Math.min(min, parseFloat(getComputedStyle(el).opacity))
    return min
  })
  const blankCharts = await page.evaluate(() => [...document.querySelectorAll('canvas')].filter((c) => {
    const ctx = c.getContext('2d')
    if (!c.width || !c.height) return true
    const data = ctx.getImageData(0, 0, c.width, c.height).data
    for (let i = 3; i < data.length; i += 16) if (data[i] > 0) return false
    return true
  }).length)
  if (blankCharts) problems.push(`${current} ${blankCharts} blank chart(s)`)
  if (hidden < 0.9) problems.push(`${current} content has opacity ${hidden}`)
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
  if (overflow > 1) problems.push(`${current} scrolls horizontally by ${overflow}px`)
  await page.screenshot({ path: `${out}/${name}${vw < 800 ? '-mobile' : ''}.png`, fullPage: vw > 800 })
  results.push(`${name.padEnd(10)} text=${text}`)
  if (text < 80) problems.push(`${current} almost empty page (${text} chars)`)
}
}
// Infrastructure: clicking a machine opens the side panel.
current = '[map drawer]'
await page.setViewport({ width: 1440, height: 1000 })
await page.goto(`${base}/projects/${slug}/map`, { waitUntil: 'networkidle0' })
const clicked = await page.evaluate(() => {
  const node = [...document.querySelectorAll('foreignObject [data-node="host"]')][0]
  node?.click()
  return !!node
})
await new Promise((r) => setTimeout(r, 1200))
const drawer = await page.evaluate(() => document.querySelector('aside.fixed')?.innerText.length || 0)
await page.screenshot({ path: `${out}/map-drawer.png` })
if (!clicked || drawer < 100) problems.push(`${current} drawer did not open (${drawer})`)

// The wins: what Slowpoke itself noticed against the window before.
current = '[wins]'
{
  const wins = await page.evaluate(async (s) => (await (await fetch(`/api/projects/${s}/slow?window=24h`)).json()).wins, slug)
  if (wins?.comparable) {
    await page.goto(`${base}/projects/${slug}/slow`, { waitUntil: 'networkidle0' })
    await new Promise((r) => setTimeout(r, 400))
    const text = await page.evaluate(() => document.body.innerText)
    if (!/vittorie|wins/i.test(text)) problems.push(`${current} the card is missing while the windows are comparable`)
  }
}

// Arranging the map by hand: drag a machine, it stays there.
current = '[map drag]'
await page.goto(`${base}/projects/${slug}/map`, { waitUntil: 'networkidle0' })
await new Promise((r) => setTimeout(r, 500))
{
  const box = await page.evaluate(() => {
    const el = [...document.querySelectorAll('foreignObject')].find((f) => f.querySelector('.cursor-move'))
    if (!el) return null
    const r = el.getBoundingClientRect()
    return { x: r.x + r.width / 2, y: r.y + 20, name: el.textContent.trim().split('\n')[0].trim() }
  })
  if (!box) problems.push(`${current} no machine to drag`)
  else {
    await page.mouse.move(box.x, box.y)
    await page.mouse.down()
    for (let i = 1; i <= 8; i++) await page.mouse.move(box.x + 20 * i, box.y + 10 * i)
    await page.mouse.up()
    await new Promise((r) => setTimeout(r, 600))
    await page.reload({ waitUntil: 'networkidle0' })
    await new Promise((r) => setTimeout(r, 600))
    const placed = await page.evaluate(async (s) => (await (await fetch(`/api/projects/${s}/topology`)).json()).nodes.filter((n) => n.x != null).length, slug)
    if (placed !== 1) problems.push(`${current} the place was not saved (${placed})`)
    await page.screenshot({ path: `${out}/map-arranged.png` })
    // Put the map back the way the demo expects it.
    await page.evaluate(async (s) => fetch(`/api/projects/${s}/topology/places`, { method: 'DELETE', headers: { 'X-Slowpoke': '1' } }), slug)
  }
}

// Install wizard from the overview. A page transform breaks position: fixed, and the popup lands
// half outside the window: what matters is that the whole card is inside it, not where exactly.
const onScreen = () => {
  const el = document.querySelector('.fixed.inset-0 .card')
  if (!el) return { ok: false, why: 'no modal' }
  const r = el.getBoundingClientRect()
  const ok = r.top >= -1 && r.left >= -1 && r.bottom <= window.innerHeight + 1 && r.right <= window.innerWidth + 1
  return { ok, why: `top ${Math.round(r.top)} bottom ${Math.round(r.bottom)} of ${window.innerHeight}` }
}

current = '[install wizard]'
await page.goto(`${base}/projects/${slug}`, { waitUntil: 'networkidle0' })
const clickText = (text) => page.evaluate((t) => {
  const b = [...document.querySelectorAll('button')].find((x) => x.innerText.includes(t))
  b?.click()
  return !!b
}, text)
if (!(await clickText('Add agent'))) problems.push(`${current} button missing`)
await new Promise((r) => setTimeout(r, 300))
{
  const box = await page.evaluate(onScreen)
  if (!box.ok) problems.push(`${current} step 1 modal is not on screen (${box.why})`)
}
await new Promise((r) => setTimeout(r, 300))
await clickText('Next')
await new Promise((r) => setTimeout(r, 900))
const wizardText = await page.evaluate(() => document.querySelector('.fixed.inset-0')?.innerText || '')
await page.screenshot({ path: `${out}/wizard.png` })
const modalBox = await page.evaluate(onScreen)
if (!modalBox.ok) problems.push(`${current} modal is not on screen (${modalBox.why})`)
if (!wizardText.includes('spa_') || !wizardText.includes('install.sh')) problems.push(`${current} step 2 misses the token or the one command`)

// Badge popover.
current = '[badge popover]'
await page.goto(`${base}/projects/${slug}`, { waitUntil: 'networkidle0' })
const badge = await page.$('.aspect-square.cursor-pointer')
await badge?.hover()
await new Promise((r) => setTimeout(r, 300))
await page.screenshot({ path: `${out}/badge-popover.png` })
const popover = await page.evaluate(() => [...document.querySelectorAll('.absolute.bottom-full')].map((x) => x.innerText).join(''))
if (!popover.includes('pts')) problems.push(`${current} popover not shown`)

// Navigation by clicking the sidebar, from every page to every other page.
current = '[sidebar clicks]'
await page.goto(`${base}/projects/${slug}/settings`, { waitUntil: 'networkidle0' })
const navHrefs = await page.evaluate(() => [...document.querySelectorAll('aside nav a')].map((a) => a.getAttribute('href')))
for (const from of navHrefs) {
  for (const to of navHrefs) {
    if (from === to) continue
    await page.goto(base + from, { waitUntil: 'networkidle0' })
    await page.evaluate((href) => [...document.querySelectorAll('aside nav a')].find((a) => a.getAttribute('href') === href).click(), to)
    await new Promise((r) => setTimeout(r, 400))
    const ok = await page.evaluate((href) => location.pathname === href && (document.querySelector('main')?.innerText.trim().length || 0) > 20, to)
    if (!ok) problems.push(`${current} ${from} -> ${to} did not navigate`)
  }
}

// Stale page after an upgrade: a missing chunk must trigger a reload, not a blank page.
current = '[stale chunk]'
const quiet = problems.length
const reloads = await page.evaluate(async () => {
  sessionStorage.removeItem('slowpoke:chunk-reload')
  return new Promise((resolve) => {
    window.addEventListener('beforeunload', () => resolve('reloaded'))
    import('/assets/does-not-exist-123.js').catch((e) => window.dispatchEvent(Object.assign(new Event('vite:preloadError', { cancelable: true }), { payload: e })))
    setTimeout(() => resolve('no reload'), 3000)
  })
})
await new Promise((r) => setTimeout(r, 1500))
problems.length = quiet // the 404 and the aborted page load are the point of this step
if (reloads !== 'reloaded') problems.push(`${current} ${reloads}`)

await browser.close()
console.log(results.join('\n'))
if (problems.length) {
  console.log('\nPROBLEMS\n' + [...new Set(problems)].join('\n'))
  process.exit(1)
}
console.log('\nall pages OK')
