import { useMemo } from 'react'
import type { BacktestFill, PriceBar } from '../api/client'
import { formatBeijingTime } from '../utils/time'

type Props = {
  bars: PriceBar[]
  fills: BacktestFill[]
}

function fmt(n: number, digits = 2) {
  return n.toLocaleString(undefined, {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  })
}

function isOpen(action: string) {
  return action.startsWith('OPEN_')
}

export function PriceChart({ bars, fills }: Props) {
  const chart = useMemo(() => {
    if (bars.length < 2) return null

    const w = 720
    const h = 260
    const pad = { top: 18, right: 16, bottom: 32, left: 56 }
    const innerW = w - pad.left - pad.right
    const innerH = h - pad.top - pad.bottom

    const times = bars.map((b) => {
      const t = new Date(b.ts).getTime()
      return Number.isNaN(t) ? 0 : t
    })
    const closes = bars.map((b) => b.close)
    const tMin = Math.min(...times)
    const tMax = Math.max(...times)
    const tSpan = Math.max(tMax - tMin, 1)

    let yMin = Math.min(...closes)
    let yMax = Math.max(...closes)
    if (yMin === yMax) {
      yMin -= 1
      yMax += 1
    }
    const yPad = (yMax - yMin) * 0.08
    yMin -= yPad
    yMax += yPad
    const ySpan = yMax - yMin

    const xAt = (t: number) => pad.left + ((t - tMin) / tSpan) * innerW
    const yAt = (v: number) => pad.top + (1 - (v - yMin) / ySpan) * innerH

    const coords = bars.map((_, i) => ({ x: xAt(times[i]), y: yAt(closes[i]) }))
    const line = coords.map((c, i) => `${i === 0 ? 'M' : 'L'}${c.x.toFixed(1)} ${c.y.toFixed(1)}`).join(' ')

    const yTicks = Array.from({ length: 5 }, (_, i) => {
      const v = yMin + (ySpan * i) / 4
      return { v, y: yAt(v) }
    })

    const markers = fills
      .map((f) => {
        const t = new Date(f.ts).getTime()
        if (Number.isNaN(t) || t < tMin || t > tMax) return null
        return {
          x: xAt(t),
          y: yAt(f.price),
          open: isOpen(f.action),
          long: f.action.includes('LONG'),
          action: f.action,
        }
      })
      .filter((m): m is NonNullable<typeof m> => m != null)

    return { w, h, pad, line, yTicks, last: bars[bars.length - 1], tMin, tMax, markers }
  }, [bars, fills])

  if (!chart) {
    return <div className="empty">暂无 K 线</div>
  }

  return (
    <div className="equity-chart">
      <div className="equity-chart-meta">
        <div className="mono muted">最新收盘 {fmt(chart.last.close)}</div>
        <div className="mono muted">
          成交 {fills.length} 笔 · {formatBeijingTime(new Date(chart.tMin).toISOString())} →{' '}
          {formatBeijingTime(new Date(chart.tMax).toISOString())}
        </div>
      </div>
      <svg viewBox={`0 0 ${chart.w} ${chart.h}`} className="equity-svg" role="img" aria-label="价格与成交">
        {chart.yTicks.map((t) => (
          <g key={t.v}>
            <line
              x1={chart.pad.left}
              x2={chart.w - chart.pad.right}
              y1={t.y}
              y2={t.y}
              className="equity-grid"
            />
            <text x={chart.pad.left - 8} y={t.y + 3} className="equity-axis" textAnchor="end">
              {fmt(t.v, 0)}
            </text>
          </g>
        ))}
        <path d={chart.line} fill="none" stroke="var(--ink)" strokeWidth="1.6" className="equity-line" />
        {chart.markers.map((m, i) => {
          const color = m.long ? 'var(--ok)' : 'var(--danger)'
          const d = m.open
            ? `M${m.x} ${m.y - 7} L${m.x - 5} ${m.y + 4} L${m.x + 5} ${m.y + 4} Z`
            : `M${m.x} ${m.y + 7} L${m.x - 5} ${m.y - 4} L${m.x + 5} ${m.y - 4} Z`
          return <path key={`${m.action}-${i}`} d={d} fill={color} opacity="0.9" />
        })}
        <text x={chart.pad.left} y={chart.h - 10} className="equity-axis">
          {formatBeijingTime(new Date(chart.tMin).toISOString())}
        </text>
        <text x={chart.w - chart.pad.right} y={chart.h - 10} className="equity-axis" textAnchor="end">
          {formatBeijingTime(new Date(chart.tMax).toISOString())}
        </text>
      </svg>
    </div>
  )
}
