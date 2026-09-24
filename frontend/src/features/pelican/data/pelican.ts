import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { apiRequest } from '@/lib/api-client';
import { getTokenFromStorage } from '@/stores/authStore';

/** Reasoning levels accepted by the backend; '' means the provider decides. */
export const PELICAN_EFFORTS = ['', 'none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'] as const;
export type PelicanEffort = (typeof PELICAN_EFFORTS)[number];

export interface PelicanTarget {
  model: string;
  effort: PelicanEffort;
}

export type PelicanStatus = 'running' | 'succeeded' | 'failed';

export interface PelicanResult {
  id: string;
  model: string;
  effort: PelicanEffort;
  status: PelicanStatus;
  createdAt: string;
  durationSeconds: number;
  format?: 'html' | 'svg';
  error?: string;
}

export interface PelicanConfigResponse {
  prompt: string;
  defaultPrompt: string;
  targets: PelicanTarget[];
  scheduleEnabled: boolean;
  nextRunAt: string;
  running: boolean;
  efforts: PelicanEffort[];
  dataDir: string;
}

export interface PelicanResultsResponse {
  results: PelicanResult[];
  running: boolean;
  nextRunAt: string;
  enabled: boolean;
  succeeded: number;
  failed: number;
  total: number;
}

export interface PelicanConversation {
  prompt: string;
  effort: PelicanEffort;
  model: string;
  reply: string;
  error?: string;
  finishReason?: string;
  usage?: Record<string, number>;
  durationSeconds: number;
  createdAt: string;
}

const authed = <T,>(endpoint: string, options: { method?: 'GET' | 'POST' | 'PUT'; body?: unknown } = {}) =>
  apiRequest<T>(endpoint, { ...options, requireAuth: true });

export const pelicanConfigKey = ['pelican', 'config'] as const;
export const pelicanResultsKey = ['pelican', 'results'] as const;

export function usePelicanConfig() {
  return useQuery({
    queryKey: pelicanConfigKey,
    queryFn: () => authed<PelicanConfigResponse>('/admin/pelican/config'),
  });
}

/**
 * The selectable models are the ones the configured channels actually serve, read from
 * `supportedModels`. The models catalogue can be empty on an instance that only routes traffic,
 * which would leave the picker without a single option.
 */
const PELICAN_MODELS_QUERY = `
  query PelicanChannelModels {
    channels(first: 200) {
      edges {
        node {
          id
          status
          supportedModels
        }
      }
    }
  }
`;

type PelicanChannelModels = {
  channels: {
    edges: { node: { id: string; status: string; supportedModels: string[] } }[];
  };
};

export function usePelicanModels() {
  return useQuery({
    queryKey: ['pelican', 'models'],
    queryFn: async () => {
      const data = await graphqlRequest<PelicanChannelModels>(PELICAN_MODELS_QUERY);
      const models = new Set<string>();
      for (const edge of data.channels?.edges ?? []) {
        // A disabled channel cannot serve a request, so its models are not offered.
        if (edge.node.status !== 'enabled') continue;
        for (const model of edge.node.supportedModels ?? []) {
          if (model.trim()) models.add(model);
        }
      }
      return [...models].sort((left, right) => left.localeCompare(right));
    },
  });
}

/** Results are polled while a round is running so the gallery fills in as models finish. */
export function usePelicanResults() {
  return useQuery({
    queryKey: pelicanResultsKey,
    queryFn: () => authed<PelicanResultsResponse>('/admin/pelican/results'),
    refetchInterval: (query) => (query.state.data?.running ? 3000 : false),
  });
}

export function useSavePelicanConfig() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (payload: { prompt: string; targets: PelicanTarget[]; scheduleEnabled: boolean }) =>
      authed<PelicanConfigResponse>('/admin/pelican/config', { method: 'PUT', body: payload }),
    onSuccess: (data) => {
      queryClient.setQueryData(pelicanConfigKey, data);
      void queryClient.invalidateQueries({ queryKey: pelicanResultsKey });
    },
  });
}

export function useRunPelicanRound() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => authed<{ started: boolean }>('/admin/pelican/rounds', { method: 'POST' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: pelicanResultsKey }),
  });
}

/**
 * Generated documents live behind the authenticated admin API, and an iframe cannot send the
 * bearer token, so the document is fetched and turned into a blob URL by the caller.
 */
export async function fetchPelicanArtifact(id: string, download = false): Promise<string> {
  const token = getTokenFromStorage();
  const response = await fetch(`/admin/pelican/results/${id}/artifact${download ? '?download=1' : ''}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });
  if (!response.ok) {
    throw new Error(`HTTP ${response.status}`);
  }
  return response.text();
}

export function usePelicanConversation(id: string | undefined, enabled: boolean) {
  return useQuery({
    queryKey: ['pelican', 'conversation', id],
    queryFn: () => authed<PelicanConversation>(`/admin/pelican/results/${id}/conversation`),
    enabled: Boolean(id) && enabled,
  });
}

/**
 * Blob URLs do not carry the response headers of the API, so the sandbox policy travels inside
 * the document. Combined with the iframe `sandbox` attribute it keeps generated code from
 * reaching the app, the network or the file system.
 */
const PREVIEW_POLICY =
  "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'";

export function buildPelicanPreviewDocument(content: string, format?: string): string {
  const meta = `<meta http-equiv="Content-Security-Policy" content="${PREVIEW_POLICY}">`;
  if (format === 'svg') {
    return `<!doctype html><html><head>${meta}<style>html,body{margin:0;height:100%;display:flex;align-items:center;justify-content:center;background:#fff}svg{max-width:100%;max-height:100%}</style></head><body>${content}</body></html>`;
  }
  if (/<head[\s>]/i.test(content)) {
    return content.replace(/<head([^>]*)>/i, `<head$1>${meta}`);
  }
  return `<!doctype html><html><head>${meta}</head><body>${content}</body></html>`;
}
