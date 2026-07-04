/**
 * Legacy notification API using class inheritance.
 */

class BaseNotifier {
  protected apiUrl: string;
  protected apiKey: string;

  constructor(apiUrl: string, apiKey: string) {
    this.apiUrl = apiUrl;
    this.apiKey = apiKey;
  }

  protected async sendRequest(endpoint: string, payload: Record<string, unknown>): Promise<Response> {
    return fetch(`${this.apiUrl}${endpoint}`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${this.apiKey}`,
      },
      body: JSON.stringify(payload),
    });
  }
}

class EmailNotifier extends BaseNotifier {
  async sendEmail(to: string, subject: string, body: string): Promise<void> {
    const resp = await this.sendRequest("/email", { to, subject, body });
    if (!resp.ok) {
      throw new Error(`Email send failed: ${resp.status}`);
    }
  }
}

class SmsNotifier extends BaseNotifier {
  async sendSms(to: string, message: string): Promise<void> {
    const resp = await this.sendRequest("/sms", { to, message });
    if (!resp.ok) {
      throw new Error(`SMS send failed: ${resp.status}`);
    }
  }
}

class PushNotifier extends BaseNotifier {
  async sendPush(deviceToken: string, title: string, body: string): Promise<void> {
    const resp = await this.sendRequest("/push", { deviceToken, title, body });
    if (!resp.ok) {
      throw new Error(`Push send failed: ${resp.status}`);
    }
  }
}

export { BaseNotifier, EmailNotifier, SmsNotifier, PushNotifier };
