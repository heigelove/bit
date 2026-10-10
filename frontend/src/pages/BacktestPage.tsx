import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, type BacktestDefaults, type BacktestResult } from '../api/client'
import { EquityChart } from '../components/EquityChart'
import { PriceChart } from '../components/PriceChart'
import { formatBeijingTime } from '../utils/time'

function pad(n: number) {
  return String(n).padStart(2, '0')
}

function toLocalInput(d: Date) {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function defaultRange() {
  const end = new Date()
  end.setMinutes(0, 0, 0)
  const start = new Date(end.getTime() - 90 * 24 * 60 * 60 * 1000)
  return { start: toLocalInput(start), end: toLocalInput(end) }
}

function fmt(n: number | undefined | null, digits = 2) {
  if (n == null || Number.isNaN(n)) return '—'
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

function timeframeHint(strategy: string, defaults: BacktestDefaults | null) {
  if (!defaults) return '使用 config.yaml 中的周期、杠杆与费率'
  const cost = `杠杆 ${defaults.leverage}x · 手续费 ${defaults.fee_rate} · 滑点 ${defaults.slippage_bps}bps`
  if (strategy === 'vegas') {
    return `${defaults.vegas_interval || '4h'} · ${cost}`
  }
  const tf = defaults.entry ? `${defaults.primary} + ${defaults.entry}` : defaults.primary
  return `${tf} · ${cost}`
}

function strategyLabel(name: string) {
  switch (name) {
    case 'squeeze':
      return 'squeeze · 压缩突破'
    case 'vegas':
      return 'vegas · 维加斯通道'
    default:
      return 'trend · 趋势回调'
  }
}

function fmtPct(n: number | undefined | null) {
  if (n == null || Number.isNaN(n)) return '—'
  const sign = n > 0 ? '+' : ''
  return `${sign}${fmt(n, 2)}%`
}

export function BacktestPage() {
  const range = useMemo(() => defaultRange(), [])
  const [defaults, setDefaults] = useState<BacktestDefaults | null>(null)
  const [strategy, setStrategy] = useState('trend')
  const [symbol, setSymbol] = useState('ETHUSDT')
  const [start, setStart] = useState(range.start)
  const [end, setEnd] = useState(range.end)
  const [balance, setBalance] = useState('10000')
  const [result, setResult] = useState<BacktestResult | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    void api.backtestDefaults().then((d) => {
      setDefaults(d)
      setStrategy(d.strategy || 'trend')
      setSymbol(d.symbol || 'ETHUSDT')
      setBalance(String(d.initial_balance || 10000))
    }).catch(() => {
      /* keep local defaults */
    })
  }, [])

  const run = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const startAt = new Date(start)
      const endAt = new Date(end)
      if (Number.isNaN(startAt.getTime()) || Number.isNaN(endAt.getTime())) {
        throw new Error('开始或结束时间无效')
      }
      const res = await api.backtest({
        strategy,
        symbol,
        start: startAt.toISOString(),
        end: endAt.toISOString(),
        initial_balance: Number(balance) || 0,
      })
      setResult(res)
    } catch (err) {
      setResult(null)
      setError(err instanceof Error ? err.message : '回测失败')
    } finally {
      setLoading(false)
    }
  }, [strategy, symbol, start, end, balance])

  const st = result?.stats
  const pnlColor = (st?.total_pnl ?? 0) >= 0 ? 'var(--ok)' : 'var(--danger)'

  return (
    <div>
      <div className="page-head">
        <div>
          <h1>策略回测</h1>
          <p>
            从币安拉取历史 K 线，按当前策略参数模拟成交 · 不启用日亏/连亏熔断
          </p>
        </div>
        <button className="btn btn-primary" type="button" disabled={loading} onClick={() => void run()}>
          {loading ? '回测中…' : '开始回测'}
        </button>
      </div>

      <section className="panel">
        <form
          className="form-grid"
          onSubmit={(e) => {
            e.preventDefault()
            void run()
          }}
        >
          <label className="field">
            <span>策略</span>
            <select value={strategy} onChange={(e) => setStrategy(e.target.value)}>
              {(defaults?.strategies ?? ['trend', 'squeeze', 'vegas']).map((name) => (
                <option key={name} value={name}>
                  {strategyLabel(name)}
                </option>
              ))}
            </select>
          </label>
          <label className="field">
            <span>品种</span>
            <input value={symbol} onChange={(e) => setSymbol(e.target.value.toUpperCase())} />
          </label>
          <label className="field">
            <span>开始</span>
            <input type="datetime-local" value={start} onChange={(e) => setStart(e.target.value)} />
          </label>
          <label className="field">
            <span>结束</span>
            <input type="datetime-local" value={end} onChange={(e) => setEnd(e.target.value)} />
          </label>
          <label className="field">
            <span>初始资金 USDT</span>
            <input type="number" min={100} step={100} value={balance} onChange={(e) => setBalance(e.target.value)} />
          </label>
          <div className="field form-hint">
            <span>参数</span>
            <p className="mono muted">
              {timeframeHint(strategy, defaults)}
            </p>
          </div>
        </form>
      </section>

      {error ? <div className="panel error-box">{error}</div> : null}

      {loading && !result ? <div className="panel empty">正在拉取 K 线并回放策略…</div> : null}

      {result && st ? (
        <>
          <div className="metrics metrics-6">
            <div className="panel metric">
              <div className="label">期末权益</div>
              <div className="value mono">{fmt(st.final_equity)}</div>
              <div className="hint">起点 {fmt(result.initial_balance)}</div>
            </div>
            <div className="panel metric">
              <div className="label">累计盈亏</div>
              <div className="value mono" style={{ color: pnlColor }}>
                {st.total_pnl >= 0 ? '+' : ''}
                {fmt(st.total_pnl)}
              </div>
              <div className="hint">{fmtPct(st.return_pct)}</div>
            </div>
            <div className="panel metric">
              <div className="label">交易次数</div>
              <div className="value mono">{st.trades}</div>
              <div className="hint">
                胜 {st.wins} / 负 {st.losses}
              </div>
            </div>
            <div className="panel metric">
              <div className="label">胜率</div>
              <div className="value mono">{fmt(st.win_rate * 100, 1)}%</div>
              <div className="hint">
                盈亏比 {st.profit_factor > 0 ? fmt(st.profit_factor, 2) : '—'}
              </div>
            </div>
            <div className="panel metric">
              <div className="label">最大回撤</div>
              <div className="value mono">{fmt(st.max_drawdown)}</div>
              <div className="hint">{fmt(st.max_drawdown_pct, 2)}%</div>
            </div>
            <div className="panel metric">
              <div className="label">K 线</div>
              <div className="value mono">{result.primary_bars}</div>
              <div className="hint">
                {result.primary}
                {result.entry_bars ? ` · ${result.entry} ${result.entry_bars}` : ''}
              </div>
            </div>
          </div>

          <section className="panel equity-panel">
            <h2 className="section-title">权益曲线</h2>
            <p className="equity-sub muted mono">
              {result.strategy} · {result.symbol} · {formatBeijingTime(result.start)} → {formatBeijingTime(result.end)}
            </p>
            <EquityChart
              points={result.equity}
              initialBalance={result.initial_balance}
              totalPnl={st.total_pnl}
            />
          </section>

          <section className="panel equity-panel">
            <h2 className="section-title">价格与成交</h2>
            <p className="equity-sub muted mono">主周期收盘价（已抽样）· 三角为开平仓</p>
            <PriceChart bars={result.price ?? []} fills={result.fills ?? []} />
          </section>

          <section className="panel">
            <h2 className="section-title">模拟成交</h2>
            <div className="table-wrap">
              <table className="data">
                <thead>
                  <tr>
                    <th>时间</th>
                    <th>动作</th>
                    <th>方向</th>
                    <th>数量</th>
                    <th>价格</th>
                    <th>手续费</th>
                    <th>盈亏</th>
                    <th>权益</th>
                    <th>原因</th>
                  </tr>
                </thead>
                <tbody>
                  {result.fills.length === 0 ? (
                    <tr>
                      <td colSpan={9} className="empty">
                        该区间无成交
                      </td>
                    </tr>
                  ) : (
                    result.fills.map((row, i) => (
                      <tr key={`${row.ts}-${i}`}>
                        <td>{formatBeijingTime(row.ts)}</td>
                        <td>{row.action}</td>
                        <td>
                          <span className={row.side === 'BUY' ? 'badge badge-buy' : 'badge badge-sell'}>
                            {row.side}
                          </span>
                        </td>
                        <td>{fmt(row.quantity, 4)}</td>
                        <td>{fmt(row.price, 2)}</td>
                        <td>{fmt(row.fee, 4)}</td>
                        <td style={{ color: row.pnl >= 0 ? 'var(--ok)' : 'var(--danger)' }}>{fmt(row.pnl, 2)}</td>
                        <td>{fmt(row.equity, 2)}</td>
                        <td title={row.reason}>{row.reason.slice(0, 48)}</td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </section>
        </>
      ) : null}
    </div>
  )
}
