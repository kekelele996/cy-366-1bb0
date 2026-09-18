import { get } from '@/utils/request'

export interface WalletTransaction {
  id: number
  user_id: number
  change_type: string
  account_type: string // balance / package
  direction: string // debit 出账 / credit 入账
  amount: number
  hours: number
  user_package_id: number
  session_id: number
  related_id: number
  balance_after: number
  remark: string
  created_at: string
}

// 资金流水（余额与时长包小时统一记账，故障中断退费可在此回读）。
export function listMyTransactions(params: { page: number; page_size: number }) {
  return get<{ list: WalletTransaction[]; total: number }>('/wallet/transactions', params)
}
