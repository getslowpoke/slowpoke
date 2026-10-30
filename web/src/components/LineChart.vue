<script setup>
import { onMounted, onBeforeUnmount, ref, watch } from 'vue'
import {
  Chart, LineController, BarController, LineElement, BarElement, PointElement, LinearScale, CategoryScale, Filler, Tooltip, Legend,
} from 'chart.js'

Chart.register(LineController, BarController, LineElement, BarElement, PointElement, LinearScale, CategoryScale, Filler, Tooltip, Legend)
Chart.defaults.font.family = '"Geist Variable", system-ui, sans-serif'
Chart.defaults.color = '#7c7c89'

const props = defineProps({
  labels: { type: Array, required: true },
  datasets: { type: Array, required: true }, // [{label, data, color, type?, fill?, axis?, dashed?}]
  height: { type: Number, default: 220 },
  yMax: { type: Number, default: undefined },
  yMin: { type: Number, default: undefined },
  formatY: { type: Function, default: (v) => v },
  markers: { type: Array, default: () => [] }, // [{index, label, color}]
  legend: { type: Boolean, default: false },
  stacked: { type: Boolean, default: false },
})

const canvas = ref(null)
let chart = null

const markerPlugin = {
  id: 'markers',
  afterDatasetsDraw(c) {
    const { ctx, chartArea, scales } = c
    let labelEnd = -Infinity // a label that would sit on the previous one is left out: the line still says it
    for (const m of props.markers) {
      const x = scales.x.getPixelForValue(m.index)
      if (x < chartArea.left || x > chartArea.right) continue
      ctx.save()
      ctx.strokeStyle = m.color || 'rgba(255,111,142,0.55)'
      ctx.setLineDash([3, 4])
      ctx.beginPath()
      ctx.moveTo(x, chartArea.top)
      ctx.lineTo(x, chartArea.bottom)
      ctx.stroke()
      ctx.setLineDash([])
      ctx.fillStyle = m.color || '#ff6f8e'
      ctx.font = '500 10px "Geist Variable", sans-serif'
      const width = ctx.measureText(m.label || '').width
      if (m.label && x + 4 > labelEnd + 6 && x + 4 + width <= chartArea.right) {
        ctx.fillText(m.label, x + 4, chartArea.top + 10)
        labelEnd = x + 4 + width
      }
      ctx.restore()
    }
  },
}

function config() {
  return {
    data: {
      labels: props.labels,
      datasets: props.datasets.map((d) => ({
        type: d.type || 'line',
        label: d.label,
        data: d.data,
        borderColor: d.color,
        backgroundColor: d.type === 'bar' ? d.color : (ctx) => {
          const { chartArea, ctx: c } = ctx.chart
          if (!chartArea) return 'transparent'
          const g = c.createLinearGradient(0, chartArea.top, 0, chartArea.bottom)
          g.addColorStop(0, d.color + '38')
          g.addColorStop(1, d.color + '00')
          return g
        },
        fill: d.fill ?? d.type !== 'bar',
        tension: 0.35,
        borderWidth: 2,
        borderDash: d.dashed ? [4, 4] : [],
        pointRadius: d.points ? 3 : 0,
        pointHoverRadius: 4,
        pointBackgroundColor: d.pointColors || d.color,
        borderRadius: d.type === 'bar' ? 4 : 0,
        maxBarThickness: 18,
        spanGaps: true,
        yAxisID: d.axis || 'y',
        stack: props.stacked ? 'stack' : undefined,
      })),
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      animation: false,
      normalized: true,
      interaction: { mode: 'index', intersect: false },
      plugins: {
        legend: { display: props.legend, labels: { boxWidth: 8, boxHeight: 8, usePointStyle: true, color: '#a6a6b2' } },
        tooltip: {
          backgroundColor: '#16161b', borderColor: '#34343e', borderWidth: 1, titleColor: '#ececf1', bodyColor: '#cfcfd8', padding: 10, cornerRadius: 10,
          callbacks: { label: (ctx) => ` ${ctx.dataset.label}: ${ctx.parsed.y === null ? '—' : props.formatY(ctx.parsed.y)}` },
        },
      },
      scales: {
        x: { stacked: props.stacked, grid: { display: false }, border: { display: false }, ticks: { maxTicksLimit: 8, maxRotation: 0 } },
        y: {
          stacked: props.stacked, min: props.yMin, max: props.yMax, grid: { color: 'rgba(255,255,255,0.04)' }, border: { display: false },
          ticks: { maxTicksLimit: 5, callback: (v) => props.formatY(v) },
        },
      },
    },
    plugins: [markerPlugin],
  }
}

onMounted(() => {
  lastKey = JSON.stringify([props.labels, props.datasets.map((d) => [d.label, d.data, d.color]), props.markers])
  chart = new Chart(canvas.value, config())
})
// Parents often pass inline arrays: only redraw when the data really changed.
let lastKey = ''
watch(() => JSON.stringify([props.labels, props.datasets.map((d) => [d.label, d.data, d.color]), props.markers]), (key) => {
  if (!chart || key === lastKey) return
  lastKey = key
  const next = config()
  chart.data = next.data
  chart.options = next.options
  chart.update()
})
onBeforeUnmount(() => chart?.destroy())
</script>

<template>
  <div :style="{ height: height + 'px' }" class="relative"><canvas ref="canvas" /></div>
</template>
