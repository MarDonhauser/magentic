/**
 * @typedef {'allow' | 'deny' | 'neutral'} NotchTone
 * @typedef {{ id: string, label: string, tone?: NotchTone }} NotchOption
 * @typedef {{ id: string, kind: 'permission' | 'question' | 'review', title: string, detail?: string, options: NotchOption[], sessionId?: string }} NotchEvent
 * @typedef {{ id: string, optionId: string }} NotchResponse
 */

export {};
