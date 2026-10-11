export type SESSendingStatus = {
  custom_endpoint?: boolean;
  account_id?: string;
  region: string;
  sending_enabled: boolean;
  production_access_enabled: boolean;
  enforcement_status?: string;
  checked_at?: number | string;
  quota: {
    max_send_rate: number;
    max_24_hour_send: number;
    sent_last_24_hours: number;
  };
};

// Amazon SES API endpoints: https://docs.aws.amazon.com/general/latest/gr/ses.html
export const sesRegions = [
  ["us-east-1", "US East (N. Virginia)"],
  ["us-east-2", "US East (Ohio)"],
  ["us-west-1", "US West (N. California)"],
  ["us-west-2", "US West (Oregon)"],
  ["af-south-1", "Africa (Cape Town)"],
  ["ap-south-2", "Asia Pacific (Hyderabad)"],
  ["ap-southeast-3", "Asia Pacific (Jakarta)"],
  ["ap-southeast-5", "Asia Pacific (Malaysia)"],
  ["ap-south-1", "Asia Pacific (Mumbai)"],
  ["ap-northeast-3", "Asia Pacific (Osaka)"],
  ["ap-northeast-2", "Asia Pacific (Seoul)"],
  ["ap-southeast-1", "Asia Pacific (Singapore)"],
  ["ap-southeast-2", "Asia Pacific (Sydney)"],
  ["ap-northeast-1", "Asia Pacific (Tokyo)"],
  ["ca-central-1", "Canada (Central)"],
  ["ca-west-1", "Canada West (Calgary)"],
  ["eu-central-1", "Europe (Frankfurt)"],
  ["eu-west-1", "Europe (Ireland)"],
  ["eu-west-2", "Europe (London)"],
  ["eu-south-1", "Europe (Milan)"],
  ["eu-west-3", "Europe (Paris)"],
  ["eu-north-1", "Europe (Stockholm)"],
  ["eu-central-2", "Europe (Zurich)"],
  ["il-central-1", "Israel (Tel Aviv)"],
  ["me-south-1", "Middle East (Bahrain)"],
  ["me-central-1", "Middle East (UAE)"],
  ["sa-east-1", "South America (São Paulo)"],
  ["us-gov-east-1", "AWS GovCloud (US-East)"],
  ["us-gov-west-1", "AWS GovCloud (US-West)"],
].map(([value, name]) => ({ value, label: `${name} (${value})` }));
