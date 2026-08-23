import { useCallback, useEffect, useState } from 'react'
import { api, type LogRow } from '../api/client'
import { formatBeijingTime } from '../utils/time'

function levelBadge(level: string) {
  const l = level.toUpperCase()
  if (l.includes('ERROR')) return 'badge badge-error'
  if (l.includes('WARN')) return 'badge badge-warn'
  return 'badge badge-info'
}

function prettyJSON(value: unknown): string {
  try {
    return JSON.stringify(value ?? {}, null, 2)
  } catch {
    return String(value)
  }
}

export function LogsPage() {
  const [items, setItems] = useState<LogRow[]>([])
  const [page, setPage] = useState(1)
  const [total, setTotal] = useState(0)
  const [level, setLevel] = useState('')
  const [error, setError] = useState('')
  const [detail, setDetail] = useState<LogRow | null>(null)
  const size = 30

  const load = useCallback(async () => {
    setError('')
    try {
      const res = await api.logs(page, size, level)
      setItems(res.items)
      setTotal(res.total)
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    }
  }, [page, level])

  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    if (!detail) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setDetail(null)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [detail])

  const pages = Math.max(1, Math.ceil(total / size))

  return (
    <div>
      <div className="page-head">
        <div>
          <h1>运行日志</h1>
          <p>来自 SQLite app_logs · 北京时间</p>
        </div>
        <button className="btn" type="button" onClick={() => void load()}>
          刷新
        </button>
      </div>

      <div className="toolbar">
        <select
          value={level}
          onChange={(e) => {
            setPage(1)
            setLevel(e.target.value)
          }}
        >
          <option value="">全部级别</option>
          <option value="INFO">INFO</option>
          <option value="WARN">WARN</option>
          <option value="ERROR">ERROR</option>
        </select>
      </div>

      {error ? <div className="panel error-box">{error}</div> : null}

      <div className="panel">
        <div className="table-wrap">
          <table className="data">
            <thead>
              <tr>
                <th>时间</th>
                <th>级别</th>
                <th>消息</th>
                <th>属性</th>
              </tr>
            </thead>
            <tbody>
              {items.length === 0 ? (
                <tr>
                  <td colSpan={4} className="empty">
                    暂无日志
                  </td>
                </tr>
              ) : (
                items.map((row) => {
                  const attrs = row.attrs || {}
                  const keys = Object.keys(attrs)
                  return (
                    <tr
                      key={row.id}
                      className="row-clickable"
                      onClick={() => setDetail(row)}
                      title="点击查看详情"
                    >
                      <td>{formatBeijingTime(row.ts)}</td>
                      <td>
                        <span className={levelBadge(row.level)}>{row.level}</span>
                      </td>
                      <td className="cell-clip">{row.msg}</td>
                      <td className="cell-clip muted">
                        {keys.length ? JSON.stringify(attrs).slice(0, 80) : '—'}
                      </td>
                    </tr>
                  )
                })
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

      {detail ? (
        <div className="modal-backdrop" onClick={() => setDetail(null)} role="presentation">
          <div
            className="modal panel"
            role="dialog"
            aria-modal="true"
            aria-labelledby="log-detail-title"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="modal-head">
              <div>
                <h2 id="log-detail-title">日志详情</h2>
                <p className="mono muted">{formatBeijingTime(detail.ts)}</p>
              </div>
              <button className="btn" type="button" onClick={() => setDetail(null)}>
                关闭
              </button>
            </div>
            <div className="modal-meta">
              <span className={levelBadge(detail.level)}>{detail.level}</span>
              <span className="mono muted">#{detail.id}</span>
            </div>
            <div className="modal-section">
              <div className="modal-label">消息</div>
              <p className="modal-msg">{detail.msg}</p>
            </div>
            <div className="modal-section">
              <div className="modal-label">属性 (JSON)</div>
              <pre className="json-block">{prettyJSON(detail.attrs)}</pre>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  )
}
