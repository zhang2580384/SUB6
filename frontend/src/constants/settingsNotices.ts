import { NEW_FEATURE_NOTICES as N } from './newFeatureNotices'

// Associate only announcements actually presented by each settings tab. Add new
// feature IDs (or explicit revisions) here so old reads cannot swallow new work.
export const SETTINGS_TAB_NOTICES: Record<string, readonly string[]> = {
  general: [N.settingsSaveDock],
  features: [N.protocolFeatureSwitches, N.smartOpsSidebar],
  gateway: [N.openai409OmitEncryptedHistory, N.deepSeekSearch, N.serverlessIPRouting,
    N.mistralBudgetUsage, N.openaiImageHeaderTimeoutGroups],
  payment: [N.bepusdtPayment, N.paymentProviderFees, N.easyPayRefundSuccessCode],
  email: [N.balanceForecast],
}

export const SIDEBAR_NOTICE = 'sub6-settings-navigation-0.1.2'
