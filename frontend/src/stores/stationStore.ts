import { defineStore } from 'pinia'
import { ref } from 'vue'
import { listAllStations, updateStationStatus, type Station } from '@/api/station'

export const useStationStore = defineStore('station', () => {
  const stations = ref<Station[]>([])

  async function loadAll() {
    stations.value = await listAllStations()
  }

  async function changeStatus(id: number, status: string) {
    const res = await updateStationStatus(id, status)
    const idx = stations.value.findIndex((s) => s.id === id)
    if (idx >= 0) {
      stations.value[idx] = res.station
    }
    return res
  }

  return { stations, loadAll, changeStatus }
})
