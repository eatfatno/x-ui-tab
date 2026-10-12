type Envelope<T> = { success: boolean; obj: T; msg?: string };
export class APIError extends Error {
  constructor(message: string, public status: number) { super(message); }
}
export async function fetchAPI<T>(endpoint: string, params: URLSearchParams, signal: AbortSignal): Promise<T> {
  const response = await fetch(`./api/v1/${endpoint}?${params.toString()}`, {
    credentials: 'same-origin', cache: 'no-store', signal,
    headers: { Accept: 'application/json' },
  });
  return readResponse<T>(response);
}

export async function deleteHistory<T>(previewToken: string, signal: AbortSignal): Promise<T> {
  const response = await fetch('./api/v1/history/delete', {
    method: 'POST', credentials: 'same-origin', cache: 'no-store', signal,
    headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'X-Monitor-CSRF': previewToken },
    body: JSON.stringify({ preview_token: previewToken }),
  });
  return readResponse<T>(response);
}

async function readResponse<T>(response: Response): Promise<T> {
  if (response.status === 401) throw new APIError('面板登录已过期，请重新登录', 401);
  const data: Envelope<T> | null = await response.json().catch(() => null);
  if (!response.ok) throw new APIError(response.status === 409 ? '预览已过期或数据已变化，请重新预览后确认' : data?.msg || (response.status === 503 ? '采集服务暂不可用，请稍后重试' : `请求失败（${response.status}）`), response.status);
  if (!data) throw new APIError('监测服务返回了无法识别的数据', response.status);
  if (!data.success) throw new APIError(data.msg || '无法读取监测数据', response.status);
  return data.obj;
}
