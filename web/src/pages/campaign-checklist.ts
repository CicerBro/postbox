type Issue = { code: string; message: string; tag?: string };
export type Checklist = {
  recipients: number;
  message_bytes: number;
  upload_bytes: number;
  duration_seconds: number;
  quota: {
    MaxSendRate?: number;
    Max24HourSend?: number;
    SentLast24Hours?: number;
  };
  fits_daily_quota: boolean;
  checks: {
    ready: boolean;
    errors: Issue[];
    warnings: Issue[];
    links: {
      url: string;
      status: string;
      http_status?: number;
      message?: string;
      personalized: boolean;
    }[];
    html_bytes: number;
    html_clipping: boolean;
    suppressed: number;
    inactive: number;
    skipped: number;
    remaining_daily_quota: number;
    user_remaining_daily_quota?: number;
    sample_recipient?: number;
  };
};
