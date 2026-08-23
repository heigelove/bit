import { useCallback, useEffect, useState } from 'react'
import { api, type AccountSnapshot, type PositionSnapshot, type RiskSnapshot } from '../api/client'
import { formatBeijingTime } from '../utils/time'

function fmt(n: number | undefined | null, digits = 2) {
  if (n == null || Number.isNaN(n)) return '—'
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

export function AccountPage() {
  const [account, setAccount] = useState<AccountSnapshot | null>(null)
  const [position, setPosition] = useState<PositionSnapshot | null>(null)
  const [risk, setRisk] = useState<RiskSnapshot | null>(null)
  const [meta, setMeta] = useState({ mode: '', symbol: '', warning: '' })
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const res = await api.account()
      setAccount(res.account)
      setPosition(res.position)
      setRisk(res.risk)
      setMeta({ mode: res.mode, symbol: res.symbol, warning: res.warning || '' })
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
    const t = setInterval(() => void load(), 10000)
    return () => clearInterval(t)
  }, [load])

  return (
    <div>
      <div className="page-head">
        <div>
          <h1>账户概览</h1>
          <p>
            {meta.symbol || 'ETHUSDT'} · {meta.mode || 'paper'} · 每 10s 刷新
          </p>
        </div>
        <button className="btn" type="button" onClick={() => void load()}>
          刷新
        </button>
      </div>

      {error ? <div className="panel error-box">{error}</div> : null}
      {meta.warning ? <div className="panel empty">{meta.warning}</div> : null}

      <div className="metrics">
        <div className="panel metric">
          <div className="label">权益 Equity</div>
          <div className="value mono">{loading ? '…' : fmt(account?.equity)}</div>
          <div className="hint">USDT</div>
        </div>
        <div className="panel metric">
          <div className="label">钱包余额</div>
          <div className="value mono">{loading ? '…' : fmt(account?.balance)}</div>
          <div className="hint">不含浮盈亏</div>
        </div>
        <div className="panel metric">
          <div className="label">未实现盈亏</div>
          <div className="value mono">{loading ? '…' : fmt(account?.unrealized_pnl)}</div>
          <div className="hint">Mark to market</div>
        </div>
        <div className="panel metric">
          <div className="label">可用</div>
          <div className="value mono">{loading ? '…' : fmt(account?.available)}</div>
          <div className="hint">Available</div>
        </div>
      </div>

      <div className="grid-2">
        <section className="panel">
          <h2 className="section-title">当前持仓</h2>
          {!position || !position.quantity ? (
            <div className="empty">空仓</div>
          ) : (
            <div className="table-wrap">
              <table className="data">
                <tbody>
                  <tr>
                    <th>方向</th>
                    <td>{position.side}</td>
                  </tr>
                  <tr>
                    <th>数量</th>
                    <td>{fmt(position.quantity, 4)}</td>
                  </tr>
                  <tr>
                    <th>开仓价</th>
                    <td>{fmt(position.entry_price)}</td>
                  </tr>
                  <tr>
                    <th>标记价</th>
                    <td>{fmt(position.mark_price)}</td>
                  </tr>
                  <tr>
                    <th>跟踪止损</th>
                    <td>{fmt(position.trail_stop)}</td>
                  </tr>
                  <tr>
                    <th>更新时间</th>
                    <td>{formatBeijingTime(position.updated_at)}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          )}
        </section>

        <section className="panel">
          <h2 className="section-title">风控状态</h2>
          {!risk ? (
            <div className="empty">暂无风控快照</div>
          ) : (
            <div className="table-wrap">
              <table className="data">
                <tbody>
                  <tr>
                    <th>当日 R</th>
                    <td>{fmt(risk.day_realized_r, 2)}</td>
                  </tr>
                  <tr>
                    <th>连续亏损</th>
                    <td>{risk.consecutive_loss}</td>
                  </tr>
                  <tr>
                    <th>熔断</th>
                    <td>{risk.halted ? `是 · ${risk.halt_reason || ''}` : '否'}</td>
                  </tr>
                  <tr>
                    <th>更新时间</th>
                    <td>{formatBeijingTime(risk.updated_at)}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          )}
        </section>
      </div>
    </div>
  )
}
