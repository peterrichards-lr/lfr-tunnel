import { request } from '@playwright/test';

export async function getMagicLinkToken(email: string): Promise<string> {
  const context = await request.newContext();
  
  // Wait for email to arrive (poll for up to 10 seconds)
  for (let i = 0; i < 20; i++) {
    const res = await context.get('http://localhost:8025/api/v1/messages');
    const data = await res.json();
    
    // Find the latest message for this email
    const messages = data.messages || [];
    const targetMsg = messages.find((m: any) => m.To && m.To[0].Address === email);
    
    if (targetMsg) {
      // Get message details to extract the body
      const msgRes = await context.get(`http://localhost:8025/api/v1/message/${targetMsg.ID}`);
      const msgData = await msgRes.json();
      
      const body = msgData.Text || '';
      // Look for magic link: /portal?token=dummy_token_123
      const match = body.match(/\/portal\?token=([a-zA-Z0-9]+)/);
      if (match && match[1]) {
        return match[1];
      }
    }
    
    // Wait 500ms before retrying
    await new Promise(resolve => setTimeout(resolve, 500));
  }
  
  throw new Error(`Magic link email not found for ${email} after 10 seconds`);
}

export async function clearMailpit() {
  const context = await request.newContext();
  await context.delete('http://localhost:8025/api/v1/messages');
}
