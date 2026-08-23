import { useCallback, useEffect, useState } from 'react'
import { api, type TradeRow } from '../api/client'
import { formatBeijingTime } from '../utils/time'

function fmt(n: number, d = 4) {
  return n.toLocaleString(undefined, { minimumFractionDigits: 0, maximumFractionDigits: d })
}

export function TradesPage() {
  const [items, setItems] = useState<TradeRow[]>([])
  const [page, setPage] = useState(1)
  const [total, setTotal] = useState(0)
  const [error, setError] = useState('')
  const size = 30

  const load = useCallback(async () => {
    setError('')
    try {
      const res = await api.trades(page, size)
      setItems(res.items)
      setTotal(res.total)
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    }
  }, [page])

  useEffect(() => {
    void load()
  }, [load])

  const pages = Math.max(1, Math.ceil(total / size))

  return (
    <div>
      <div className="page-head">
        <div>
          <h1>交易记录</h1>
          <p>来自 SQLite trades · 北京时间</p>
        </div>
        <button className="btn" type="button" onClick={() => void load()}>
          刷新
        </button>
      </div>

      {error ? <div className="panel error-box">{error}</div> : null}

      <div className="panel">
        <div className="table-wrap">
          <table className="data">
            <thead>
              <tr>
                <th>时间</th>
                <th>品种</th>
                <th>方向</th>
                <th>数量</th>
                <th>价格</th>
                <th>手续费</th>
                <th>盈亏</th>
                <th>原因</th>
                <th>模式</th>
              </tr>
            </thead>
            <tbody>
              {items.length === 0 ? (
                <tr>
                  <td colSpan={9} className="empty">
                    暂无成交
                  </td>
                </tr>
              ) : (
                items.map((row) => (
                  <tr key={row.id}>
                    <td>{formatBeijingTime(row.ts)}</td>
                    <td>{row.symbol}</td>
                    <td>
                      <span className={row.side === 'BUY' ? 'badge badge-buy' : 'badge badge-sell'}>
                        {row.side}
                      </span>
                    </td>
                    <td>{fmt(row.quantity)}</td>
                    <td>{fmt(row.price, 2)}</td>
                    <td>{fmt(row.fee, 4)}</td>
                    <td style={{ color: row.pnl >= 0 ? 'var(--ok)' : 'var(--danger)' }}>
                      {fmt(row.pnl, 2)}
                    </td>
                    <td title={row.reason}>{row.reason.slice(0, 40)}</td>
                    <td>{row.mode}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
        <div className="pager">
          <span>
            共 {total} 条 · 第 {page}/{pages} 页
          </span>
          <div className="actions">
            <button className="btn" type="button" disabled={page <= 1} onClick={() => setPage((p) => p - 1)}>
              上一页
            </button>
            <button
              className="btn"
              type="button"
              disabled={page >= pages}
              onClick={() => setPage((p) => p + 1)}
            >
              下一页
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
