// 分组 store。
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/http'
import type { Group } from '@/types'

export const useGroupsStore = defineStore('groups', () => {
  const groups = ref<Group[]>([])
  const loading = ref(false)

  async function load() {
    loading.value = true
    try {
      groups.value = await api.listGroups()
    } finally {
      loading.value = false
    }
  }

  async function create(name: string, color = '') {
    const g = await api.createGroup({ name, color })
    groups.value.push(g)
    return g
  }

  async function update(id: string, body: Partial<Group>) {
    const g = await api.updateGroup(id, body)
    const idx = groups.value.findIndex((x) => x.id === id)
    if (idx >= 0) groups.value[idx] = g
    return g
  }

  async function remove(id: string) {
    await api.deleteGroup(id)
    groups.value = groups.value.filter((g) => g.id !== id)
  }

  return { groups, loading, load, create, update, remove }
})
