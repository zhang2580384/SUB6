export const ufPromoKeys = {
  sol: 'openai_gpt62_sol_uf_promotion',
  astra: 'openai_gpt62_astra_uf_promotion',
} as const
export type UFPromoModel = keyof typeof ufPromoKeys
export type UFPromotions = Record<UFPromoModel, boolean>

export function readUFPromotions(extra?: Record<string, unknown> | null): UFPromotions {
  return { sol: extra?.[ufPromoKeys.sol] === true, astra: extra?.[ufPromoKeys.astra] === true }
}

export function writeUFPromotions(extra: Record<string, unknown>, value: UFPromotions) {
  for (const model of Object.keys(ufPromoKeys) as UFPromoModel[]) {
    if (value[model]) extra[ufPromoKeys[model]] = true
    else delete extra[ufPromoKeys[model]]
  }
}
