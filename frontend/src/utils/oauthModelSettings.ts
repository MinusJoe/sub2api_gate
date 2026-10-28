// These OAuth accounts use the same account-level model_mapping contract.
export function supportsOAuthModelSettings(account: { platform: string; type: string }): boolean {
  return (account.type === 'oauth' && ['openai', 'grok', 'anthropic', 'kimi'].includes(account.platform)) ||
    (account.platform === 'anthropic' && account.type === 'setup-token')
}
