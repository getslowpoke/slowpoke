/**
 * The providers people already have. Those with an API need only a key: HTTPS always gets out,
 * while many clouds close the SMTP ports of a new server. The others fill host and port.
 */
export const mailProviders = [
  {
    id: 'brevo', name: 'Brevo', api: true,
    hint: 'Brevo → SMTP & API → API keys: create a key. The sender must be an address or a domain verified in Brevo.',
  },
  {
    id: 'resend', name: 'Resend', api: true,
    hint: 'Resend → API Keys: create a key with sending access. The sender must be on a domain verified in Resend.',
  },
  {
    id: 'postmark', name: 'Postmark', api: true,
    hint: 'Postmark → your server → API Tokens: the Server API token. The sender must be a verified signature or domain.',
  },
  {
    id: 'sendgrid', name: 'SendGrid', api: true,
    hint: 'SendGrid → Settings → API Keys: a key with Mail Send access. The sender must be verified in SendGrid.',
  },
  {
    id: 'mailgun', name: 'Mailgun', api: true, needs: ['domain', 'region'],
    hint: 'Mailgun → API Security: a sending key. The domain is the one you send from (e.g. mg.example.com).',
  },
  {
    id: 'cloudflare', name: 'Cloudflare', api: true, needs: ['account_id'],
    hint: 'Needs the Workers Paid plan. Onboard the sending domain under Email Service → Email Sending, create an API token with "Email Sending: Edit"; the account ID is on the right of the domain overview.',
  },
  {
    id: 'google', name: 'Google Workspace / Gmail', host: 'smtp.gmail.com', port: '587',
    hint: 'The username is the full address; the password is an app password (Google account → Security → App passwords), not the usual one.',
  },
  {
    id: 'ses', name: 'Amazon SES', host: 'email-smtp.eu-west-1.amazonaws.com', port: '587',
    hint: 'Change the region in the server name to yours. SES → SMTP settings → Create SMTP credentials gives username and password.',
  },
  { id: 'smtp', name: 'Other SMTP server', host: '', port: '587', hint: '' },
]

/** The preset a saved account belongs to. */
export function providerFor(account) {
  const kind = account?.provider || ''
  if (kind && kind !== 'smtp') return mailProviders.find((p) => p.id === kind) || null
  const h = String(account?.host || '').toLowerCase()
  if (!h) return null
  if (h.startsWith('email-smtp.') && h.endsWith('.amazonaws.com')) return mailProviders.find((p) => p.id === 'ses')
  return mailProviders.find((p) => !p.api && p.host === h) || mailProviders.find((p) => p.id === 'smtp')
}
