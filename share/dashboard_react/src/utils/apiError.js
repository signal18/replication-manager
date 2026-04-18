const NETWORK_ERROR_FALLBACK = "Network error. Please check your connection and retry.";

const SENSITIVE_KEY_RE = /(secret|secretkey|password|token|accesskey|apikey|api[_-]?key|authorization)/i;

const sanitizeObject = (value) => {
  if (Array.isArray(value)) {
    return value.map((item) => sanitizeObject(item));
  }
  if (!value || typeof value !== "object") {
    return value;
  }

  return Object.entries(value).reduce((acc, [key, nextValue]) => {
    if (SENSITIVE_KEY_RE.test(key)) {
      acc[key] = "[REDACTED]";
      return acc;
    }
    acc[key] = sanitizeObject(nextValue);
    return acc;
  }, {});
};

export const redactSensitiveInfo = (input) => {
  if (input === null || input === undefined) {
    return "";
  }

  if (typeof input === "object") {
    try {
      return JSON.stringify(sanitizeObject(input));
    } catch {
      return "[REDACTED]";
    }
  }

  const text = String(input);
  return text
    .replace(/((?:secret|secretkey|password|token|accesskey|apikey|api[_-]?key|authorization)\s*[=:]\s*)([^\s,;]+)/gi, "$1[REDACTED]")
    .replace(/("(?:secret|secretkey|password|token|accesskey|apikey|api[_-]?key|authorization)"\s*:\s*")([^"]+)(")/gi, "$1[REDACTED]$3")
    .replace(/(Bearer\s+)([^\s,;]+)/gi, "$1[REDACTED]");
};

const pickMessageFromObject = (payload) => {
  if (!payload || typeof payload !== "object") return "";

  const preferredKeys = ["errorMessage", "message", "error", "detail", "title"];
  for (const key of preferredKeys) {
    const candidate = payload?.[key];
    if (typeof candidate === "string" && candidate.trim()) {
      return candidate.trim();
    }
  }

  return "";
};

const pickMessageFromPayload = (payload) => {
  if (!payload) return "";

  if (typeof payload === "string") {
    const trimmed = payload.trim();
    if (!trimmed) return "";
    try {
      const parsed = JSON.parse(trimmed);
      const parsedMsg = pickMessageFromObject(parsed);
      if (parsedMsg) return parsedMsg;
    } catch {
      // text payload, not JSON
    }
    return trimmed;
  }

  if (typeof payload === "object") {
    const nested = pickMessageFromObject(payload);
    if (nested) return nested;
    return redactSensitiveInfo(payload);
  }

  return "";
};

export const extractApiErrorMessage = (error, fallbackMessage = "Request failed.") => {
  if (!error) return fallbackMessage;

  if (typeof error === "string") {
    return redactSensitiveInfo(error) || fallbackMessage;
  }

  const responsePayload = error?.response?.data;
  const responseMessage = pickMessageFromPayload(responsePayload);
  if (responseMessage) {
    return redactSensitiveInfo(responseMessage) || fallbackMessage;
  }

  const networkish = !!error?.request && !error?.response;
  if (networkish || error?.code === "ERR_NETWORK") {
    return NETWORK_ERROR_FALLBACK;
  }

  const directMessage = error?.errorMessage || error?.message;
  if (typeof directMessage === "string" && directMessage.trim()) {
    return redactSensitiveInfo(directMessage.trim()) || fallbackMessage;
  }

  return fallbackMessage;
};
