import { get, post, put, del } from '@/utils/request'
import type { Session } from '@/api/session'

export interface Station {
  id: number
  name: string
  area: string
  station_type: string
  price_per_hour: number
  status: string
  description: string
}

export function listStations(params: { page: number; page_size: number; area?: string; status?: string }) {
  return get<{ list: Station[]; total: number; page: number; page_size: number }>('/stations', params)
}

export function listAllStations() {
  return get<Station[]>('/stations/all')
}

export function getStation(id: number) {
  return get<Station>(`/stations/${id}`)
}

export function createStation(data: Partial<Station>) {
  return post<Station>('/stations', data)
}

export function updateStation(id: number, data: Partial<Station>) {
  return put<Station>(`/stations/${id}`, data)
}

export function updateStationStatus(id: number, status: string) {
  return put<Station>(`/stations/${id}/status`, { status })
}

// 标记故障：使用中机位移交后端中断上机、按实际分钟结算退费，机位停在故障。
export function markStationFault(id: number) {
  return post<FaultInterruptResult>(`/stations/${id}/fault`, {})
}

export interface FaultInterruptResult {
  station: Station
  session: Session | null
  interrupted: boolean
  actual_minutes: number
  actual_amount: number
  refund_balance: number
  refund_hours: number
  expired_hours: number
  expired_balance: number
}

export function deleteStation(id: number) {
  return del(`/stations/${id}`)
}
