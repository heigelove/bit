export type AccountSnapshot = {
  mode: string
  symbol: string
  balance: number
  available: number
  unrealized_pnl: number
  equity: number
  updated_at: string
}

export type PositionSnapshot = {
  mode: string
  symbol: string
  side: string
  quantity: number
  entry_price: number
  mark_price: number
  trail_stop: number
  updated_at: string
}

export type RiskSnapshot = {
  day_realized_r: number
  consecutive_loss: number
  halted: boolean
  halt_reason: string
  updated_at: string
}

export type PageResult<T> = {
  items: T[]
  total: number
  page: number
  size: number
}

export type LogRow = {
  id: number
  ts: string
  level: string
  msg: string
  attrs: Record<string, unknown>
}

export type TradeRow = {
  id: number
  ts: string
  symbol: string
  side: string
  quantity: number
  price: number
  fee: number
  pnl: number
  reason: string
  mode: string
  order_id: string
}

export type EquityPoint = {
  ts: string
  equity: number
  cumulative_pnl: number
  trade_pnl: number
}

export type EquityCurve = {
  initial_balance: number
  total_pnl: number
  points: EquityPoint[]
}

const TOKEN_KEY = 'bit_token'

export function getToken() {
  return localStorage.getItem(TOKEN_KEY)
}

export function setToken(token: string | null) {
  if (token) localStorage.setItem(TOKEN_KEY, token)
  else localStorage.removeItem(TOKEN_KEY)
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Content-Type', 'application/json')
  const token = getToken()
  if (token) headers.set('Authorization', `Bearer ${token}`)

  const res = await fetch(path, { ...init, headers })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new Error((data as { error?: string }).error || `HTTP ${res.status}`)
  }
  return data as T
}

export const api = {
  login(username: string, password: string) {
    return request<{ token: string; username: string }>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    })
  },
  me() {
    return request<{ username: string }>('/api/auth/me')
  },
  account() {
    return request<{
      mode: string
      symbol: string
      account: AccountSnapshot | null
      position: PositionSnapshot | null
      risk: RiskSnapshot | null
      warning?: string
    }>('/api/account')
  },
  equityCurve(symbol = '', mode = '') {
    const q = new URLSearchParams()
    if (symbol) q.set('symbol', symbol)
    if (mode) q.set('mode', mode)
    q.set('limit', '500')
    const qs = q.toString()
    return request<{ mode: string; symbol: string; curve: EquityCurve }>(
      `/api/equity-curve${qs ? `?${qs}` : ''}`,
    )
  },
  logs(page: number, size: number, level = '') {
    const q = new URLSearchParams({ page: String(page), size: String(size) })
    if (level) q.set('level', level)
    return request<PageResult<LogRow>>(`/api/logs?${q}`)
  },
  trades(page: number, size: number, symbol = '') {
    const q = new URLSearchParams({ page: String(page), size: String(size) })
    if (symbol) q.set('symbol', symbol)
    return request<PageResult<TradeRow>>(`/api/trades?${q}`)
  },
  backtestDefaults() {
    return request<BacktestDefaults>('/api/backtest/defaults')
  },
  backtest(body: BacktestRequest) {
    return request<BacktestResult>('/api/backtest', {
      method: 'POST',
      body: JSON.stringify(body),
    })
  },
}

export type BacktestDefaults = {
  symbol: string
  strategy: string
  strategies: string[]
  primary: string
  entry: string
  initial_balance: number
  fee_rate: number
  slippage_bps: number
  min_bars: number
  leverage: number
}

export type BacktestRequest = {
  strategy: string
  symbol: string
  start: string
  end: string
  initial_balance: number
}

export type PriceBar = {
  ts: string
  open: number
  high: number
  low: number
  close: number
}

export type BacktestFill = {
  ts: string
  side: string
  action: string
  quantity: number
  price: number
  fee: number
  pnl: number
  reason: string
  equity: number
}

export type BacktestStats = {
  final_equity: number
  total_pnl: number
  return_pct: number
  trades: number
  wins: number
  losses: number
  win_rate: number
  profit_factor: number
  avg_win: number
  avg_loss: number
  max_drawdown: number
  max_drawdown_pct: number
  max_consecutive_loss: number
  fees: number
}

export type BacktestResult = {
  strategy: string
  symbol: string
  primary: string
  entry: string
  start: string
  end: string
  primary_bars: number
  entry_bars: number
  initial_balance: number
  stats: BacktestStats
  equity: EquityPoint[]
  price: PriceBar[]
  fills: BacktestFill[]
}
