import { useMemo } from 'react'
import type { EquityPoint } from '../api/client'
import { formatBeijingTime } from '../utils/time'

type Props = {
  points: EquityPoint[]
  initialBalance: number
  totalPnl: number
}

function fmt(n: number, digits = 2) {
  return n.toLocaleString(undefined, {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  })
}

export function EquityChart({ points, initialBalance, totalPnl }: Props) {
  const chart = useMemo(() => {
    if (points.length < 2) return null

    const w = 720
    const h = 260
    const pad = { top: 18, right: 16, bottom: 32, left: 56 }
    const innerW = w - pad.left - pad.right
    const innerH = h - pad.top - pad.bottom

    const times = points.map((p) => {
      const t = new Date(p.ts).getTime()
      return Number.isNaN(t) ? 0 : t
    })
    const equities = points.map((p) => p.equity)
    const tMin = Math.min(...times)
    const tMax = Math.max(...times)
    const tSpan = Math.max(tMax - tMin, 1)

    let yMin = Math.min(...equities, initialBalance)
    let yMax = Math.max(...equities, initialBalance)
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

    const coords = points.map((_, i) => ({ x: xAt(times[i]), y: yAt(equities[i]) }))
    const line = coords.map((c, i) => `${i === 0 ? 'M' : 'L'}${c.x.toFixed(1)} ${c.y.toFixed(1)}`).join(' ')
    const baselineY = yAt(initialBalance)
    const area = `${line} L${coords[coords.length - 1].x.toFixed(1)} ${baselineY.toFixed(1)} L${coords[0].x.toFixed(1)} ${baselineY.toFixed(1)} Z`

    const yTicks = Array.from({ length: 5 }, (_, i) => {
      const v = yMin + (ySpan * i) / 4
      return { v, y: yAt(v) }
    })

    return {
      w,
      h,
      pad,
      line,
      area,
      yTicks,
      baselineY,
      last: points[points.length - 1],
      lastCoord: coords[coords.length - 1],
      tMin,
      tMax,
      up: totalPnl >= 0,
    }
  }, [points, initialBalance, totalPnl])

  if (!chart) {
    return <div className="empty">暂无成交，收益曲线待生成</div>
  }

  const stroke = chart.up ? 'var(--ok)' : 'var(--danger)'
  const fill = chart.up ? 'rgba(15, 110, 86, 0.14)' : 'rgba(180, 35, 24, 0.12)'

  return (
    <div className="equity-chart">
      <div className="equity-chart-meta">
        <div>
          <span className="muted">累计盈亏</span>
          <strong className="mono" style={{ color: stroke, marginLeft: 8 }}>
            {totalPnl >= 0 ? '+' : ''}
            {fmt(totalPnl)}
          </strong>
        </div>
        <div className="mono muted">
          最新权益 {fmt(chart.last.equity)} · 起点 {fmt(initialBalance)}
        </div>
      </div>
      <svg viewBox={`0 0 ${chart.w} ${chart.h}`} className="equity-svg" role="img" aria-label="收益曲线">
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
        <line
          x1={chart.pad.left}
          x2={chart.w - chart.pad.right}
          y1={chart.baselineY}
          y2={chart.baselineY}
          className="equity-baseline"
        />
        <path d={chart.area} fill={fill} className="equity-area" />
        <path d={chart.line} fill="none" stroke={stroke} strokeWidth="2.2" className="equity-line" />
        <circle cx={chart.lastCoord.x} cy={chart.lastCoord.y} r="4" fill={stroke} />
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
