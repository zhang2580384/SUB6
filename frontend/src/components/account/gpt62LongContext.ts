export const gpt62LongContextModels = [
  { id: 'sol', name: 'GPT-6.2 Sol', key: 'openai_gpt62_sol_long_context' },
  { id: 'astra', name: 'GPT-6.2 Astra', key: 'openai_gpt62_astra_long_context' },
] as const

const overrideKey = 'openai_model_long_context_override'
export type GPT62LongContext = Record<'enabled' | typeof gpt62LongContextModels[number]['id'], boolean>

export function readGPT62LongContext(extra?: Record<string, unknown> | null): GPT62LongContext {
  return {
    enabled: extra?.[overrideKey] === true,
    sol: extra?.openai_gpt62_sol_long_context === true,
    astra: extra?.openai_gpt62_astra_long_context === true,
  }
}

export function writeGPT62LongContext(extra: Record<string, unknown>, flags: GPT62LongContext) {
  if (flags.enabled || overrideKey in extra) extra[overrideKey] = flags.enabled
  for (const { id, key } of gpt62LongContextModels) {
    // An omitted key preserves the current value on partial updates.
    if (flags[id] || key in extra) extra[key] = flags[id]
  }
}
