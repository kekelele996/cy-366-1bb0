import { get, post } from '@/utils/request'

export interface Session {
  id: number
  user_id: number
  station_id: number
  start_time: string
  end_time: string | null
  duration_minutes: number
  game_type: string
  amount: number
  status: string
}

export interface RankItem {
  rank: number
  user_id: number
  username: string
  nickname: string
  total_minutes: number
}

export interface WalletFlow {
  id: number
  user_id: number
  session_id: number
  user_package_id: number
  direction: 'deduct' | 'refund'
  kind: 'balance' | 'package_hours'
  biz_type: string
  amount: number
  hours: number
  remark: string
  created_at: string
}

export function listSessions(params: { page: number; page_size: number; status?: string; user_id?: number }) {
  return get<{ list: Session[]; total: number }>('/sessions', params)
}

export function startSession(data: { station_id: number; reservation_id?: number; game_type?: string }) {
  return post<Session>('/sessions', data)
}

export function renewSession(id: number, add_minutes: number) {
  return post<Session>(`/sessions/${id}/renew`, { add_minutes })
}

export function endSession(id: number, game_type?: string) {
  return post<Session>(`/sessions/${id}/end`, { game_type })
}

export function getRank(params: { period?: string; game_type?: string; limit?: number }) {
  return get<RankItem[]>('/sessions/rank', params)
}

export function getSessionFlows(id: number) {
  return get<WalletFlow[]>(`/sessions/${id}/flows`)
}
