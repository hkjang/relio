export type Setting = { namespace: string; key: string; value: unknown; valueType: string; secret: boolean; configured: boolean; restartRequired: boolean; version: number }
export type SettingChange = Pick<Setting, 'namespace' | 'key' | 'value'> & Partial<Setting>

// Never send the masked secret back, or silently discard concurrency metadata.
export function settingChanges(items: Setting[], changes: SettingChange[]): SettingChange[] {
  return changes.flatMap(change => {
    const old = items.find(x => x.namespace === change.namespace && x.key === change.key)
    if ((change.secret || old?.secret) && (change.value === '' || change.value == null)) return []
    if (old && !old.secret && JSON.stringify(old.value) === JSON.stringify(change.value)) return []
    return [{ ...change, version: old?.version ?? 0, secret: old?.secret || change.secret || false,
      restartRequired: old?.restartRequired ?? change.restartRequired ?? false,
      valueType: old?.valueType || change.valueType || (typeof change.value === 'object' ? 'json' : typeof change.value) }]
  })
}

export async function persistSettings(items: Setting[], changes: SettingChange[], write: (change: SettingChange) => Promise<unknown>) {
  const pending = settingChanges(items, changes)
  // This API saves one key at a time. Wait for EVERY write before reporting
  // partial failure, so a retry/reload cannot race with an unfinished write.
  const results = await Promise.allSettled(pending.map(write))
  const failed = results.flatMap((result, i) => result.status === 'rejected' ? [{ key: `${pending[i].namespace}.${pending[i].key}`, reason: result.reason instanceof Error ? result.reason.message : '요청 실패' }] : [])
  if (failed.length) throw new Error(`설정 ${pending.length - failed.length}/${pending.length}개 저장, ${failed.length}개 실패. 입력은 보존했습니다. 저장된 값을 다시 불러와 확인하세요.\n${failed.map(x => `${x.key}: ${x.reason}`).join('\n')}`)
}
