import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { IconAlertTriangle, IconDownload } from '@tabler/icons-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { cn } from '@/lib/utils';
import {
  buildPelicanPreviewDocument,
  fetchPelicanArtifact,
  usePelicanConversation,
  type PelicanResult,
} from '../data/pelican';

const effortLabel = (t: (key: string) => string, effort: string) =>
  effort ? t(`pelican.efforts.${effort}`) : t('pelican.efforts.auto');

/**
 * Logical viewport the generated document is rendered at. Generated pages size themselves from the
 * viewport (canvas drawings read innerWidth/innerHeight), so a fixed page keeps the results
 * comparable, and the card can scale that whole page down instead of cropping it.
 */
const PAGE_WIDTH = 1000;
const PAGE_HEIGHT = 750;

/** Tracks the card width so the page above it always fits, whatever the grid column count. */
function useFitScale(enabled: boolean) {
  const reference = useRef<HTMLDivElement>(null);
  const [scale, setScale] = useState(1);

  useEffect(() => {
    const element = reference.current;
    if (!enabled || !element) return;
    const update = () => setScale(element.clientWidth / PAGE_WIDTH);
    update();
    const observer = new ResizeObserver(update);
    observer.observe(element);
    return () => observer.disconnect();
  }, [enabled]);

  return { reference, scale };
}

/**
 * Loads a generated document through the authenticated API and renders it in a sandboxed iframe.
 *
 * `mode="thumbnail"` shows the whole page scaled to the card, where the card owns the click;
 * `mode="full"` renders at the page's own size and stays interactive.
 */
export function PelicanPreviewFrame({
  result,
  className,
  mode = 'full',
}: {
  result: PelicanResult;
  className?: string;
  mode?: 'thumbnail' | 'full';
}) {
  const { t } = useTranslation();
  const [url, setUrl] = useState<string>();
  const [failed, setFailed] = useState(false);
  const { reference, scale } = useFitScale(mode === 'thumbnail');

  useEffect(() => {
    let objectUrl: string | undefined;
    let cancelled = false;
    setUrl(undefined);
    setFailed(false);

    if (result.status !== 'succeeded' || !result.format) return;

    void (async () => {
      try {
        const content = await fetchPelicanArtifact(result.id);
        if (cancelled) return;
        const document = buildPelicanPreviewDocument(content, result.format);
        objectUrl = URL.createObjectURL(new Blob([document], { type: 'text/html' }));
        setUrl(objectUrl);
      } catch {
        if (!cancelled) setFailed(true);
      }
    })();

    return () => {
      cancelled = true;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [result.id, result.status, result.format]);

  // The wrapper is rendered before the document finishes loading, so the measurement below has a
  // box to observe from the first frame on.
  const content = (() => {
    if (result.status === 'running') {
      return <Skeleton className="h-full w-full" />;
    }
    if (failed || result.status !== 'succeeded') {
      // A failed attempt is a result of its own: it must read as a failure, not as an empty frame.
      return (
        <div className="flex h-full w-full flex-col items-center justify-center gap-2 bg-destructive/5 p-4 text-center">
          <IconAlertTriangle className="size-7 text-destructive/80" />
          <span className="text-xs font-medium text-destructive">{t('pelican.card.failed')}</span>
          {result.error ? (
            <span className="line-clamp-4 text-[11px] leading-relaxed text-muted-foreground">{result.error}</span>
          ) : null}
        </div>
      );
    }
    if (!url) {
      return <Skeleton className="h-full w-full" />;
    }
    return (
      <iframe
        title={`${result.channelName ? `${result.channelName} ` : ''}${result.model} ${result.effort}`}
        src={url}
        sandbox="allow-scripts"
        referrerPolicy="no-referrer"
        style={
          mode === 'thumbnail'
            ? { width: PAGE_WIDTH, height: PAGE_HEIGHT, transform: `scale(${scale})`, transformOrigin: 'top left' }
            : undefined
        }
        className={cn('border-0 bg-white', mode === 'thumbnail' ? 'pointer-events-none' : 'h-full w-full')}
      />
    );
  })();

  if (mode !== 'thumbnail') return content;

  return (
    <div ref={reference} className={cn('h-full w-full overflow-hidden', className)}>
      {content}
    </div>
  );
}

export function PelicanCard({ result, onOpen }: { result: PelicanResult; onOpen: () => void }) {
  const { t } = useTranslation();
  const time = useMemo(() => new Date(result.createdAt).toLocaleString(), [result.createdAt]);
  // Two rows of the same model on different channels must stay distinguishable.
  const title = result.channelName ? `${result.channelName} · ${result.model}` : result.model;

  return (
    <Card className="gap-0 overflow-hidden p-0">
      <button type="button" onClick={onOpen} className="w-full cursor-pointer text-left">
        <div className="flex items-center justify-between gap-2 border-b px-3 py-2">
          <span className="truncate text-sm font-medium" title={title}>
            {title}
          </span>
          <Badge variant="secondary" className="shrink-0 text-[11px]">
            {effortLabel(t, result.effort)}
          </Badge>
        </div>
        <div className="aspect-[4/3] w-full overflow-hidden bg-muted/20">
          <PelicanPreviewFrame result={result} mode="thumbnail" />
        </div>
        <div className="flex items-center justify-between gap-2 px-3 py-2 text-xs text-muted-foreground">
          <span>{time}</span>
          <span>{result.durationSeconds ? `${result.durationSeconds.toFixed(1)}s` : '-'}</span>
        </div>
      </button>
    </Card>
  );
}

export function PelicanPreviewDialog({
  results,
  index,
  onIndexChange,
  onClose,
}: {
  results: PelicanResult[];
  index: number;
  onIndexChange: (index: number) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const result = results[index];
  const [tab, setTab] = useState('preview');
  const conversation = usePelicanConversation(result?.id, tab === 'conversation');

  const download = async () => {
    if (!result) return;
    const content = await fetchPelicanArtifact(result.id, true);
    const url = URL.createObjectURL(new Blob([content], { type: 'text/plain' }));
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = `${result.id}.${result.format ?? 'html'}`;
    anchor.click();
    URL.revokeObjectURL(url);
  };

  if (!result) return null;

  return (
    <Dialog open onOpenChange={(open) => (!open ? onClose() : undefined)}>
      <DialogContent className="flex h-[85vh] max-w-4xl flex-col gap-0 p-0">
        {/* pr-12 keeps the actions clear of the close button DialogContent renders itself. */}
        <DialogHeader className="flex-row items-center justify-between gap-3 border-b px-4 py-3 pr-12">
          <div className="min-w-0">
            <DialogTitle className="truncate text-base">
              {result.channelName ? `${result.channelName} · ` : ''}
              {result.model} · {effortLabel(t, result.effort)}
            </DialogTitle>
            <p className="mt-1 text-xs text-muted-foreground">
              {new Date(result.createdAt).toLocaleString()} · {result.durationSeconds.toFixed(1)}s
            </p>
          </div>
          <Button variant="outline" size="sm" onClick={() => void download()} disabled={result.status !== 'succeeded'}>
            <IconDownload className="size-4" />
            {t('pelican.preview.download')}
          </Button>
        </DialogHeader>

        <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col">
          <TabsList className="mx-4 mt-3 w-fit">
            <TabsTrigger value="preview">{t('pelican.preview.tabPreview')}</TabsTrigger>
            <TabsTrigger value="conversation">{t('pelican.preview.tabConversation')}</TabsTrigger>
          </TabsList>

          <TabsContent value="preview" className="min-h-0 flex-1 px-4 pb-4">
            <div className="h-full overflow-hidden rounded-md border bg-muted/20">
              {result.status === 'succeeded' ? (
                <PelicanPreviewFrame result={result} />
              ) : (
                <div className="flex h-full flex-col items-center justify-center gap-2 p-6 text-center">
                  <IconAlertTriangle className="size-7 text-destructive/80" />
                  <span className="text-sm font-medium text-destructive">{t('pelican.card.failed')}</span>
                  {result.error ? <span className="text-xs text-muted-foreground">{result.error}</span> : null}
                </div>
              )}
            </div>
          </TabsContent>

          <TabsContent value="conversation" className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
            {conversation.isLoading ? (
              <Skeleton className="h-24 w-full" />
            ) : conversation.data ? (
              <div className="flex flex-col gap-4">
                <section className="flex flex-col gap-2">
                  <h4 className="text-xs font-semibold text-muted-foreground">{t('pelican.preview.prompt')}</h4>
                  <pre className="rounded-md border bg-muted/20 p-3 text-xs leading-relaxed whitespace-pre-wrap">{conversation.data.prompt}</pre>
                </section>
                <section className="flex flex-col gap-2">
                  <h4 className="text-xs font-semibold text-muted-foreground">
                    {conversation.data.reply ? t('pelican.preview.reply') : t('pelican.preview.failure')}
                  </h4>
                  <pre
                    className={cn(
                      'rounded-md border bg-muted/20 p-3 text-xs leading-relaxed whitespace-pre-wrap',
                      conversation.data.reply ? '' : 'text-destructive'
                    )}
                  >
                    {conversation.data.reply || conversation.data.error || t('pelican.preview.failure')}
                  </pre>
                </section>
                <p className="text-xs text-muted-foreground">
                  {conversation.data.finishReason ? `${t('pelican.preview.finishReason')}: ${conversation.data.finishReason}` : null}
                  {conversation.data.usage?.total_tokens ? ` · tokens: ${conversation.data.usage.total_tokens}` : null}
                </p>
              </div>
            ) : (
              <p className="text-sm text-muted-foreground">{t('pelican.preview.noConversation')}</p>
            )}
          </TabsContent>
        </Tabs>

        <div className="flex items-center justify-between border-t px-4 py-3">
          <Button variant="outline" size="sm" disabled={index === 0} onClick={() => onIndexChange(index - 1)}>
            {t('pelican.preview.previous')}
          </Button>
          <span className="text-xs text-muted-foreground">
            {index + 1} / {results.length}
          </span>
          <Button variant="outline" size="sm" disabled={index >= results.length - 1} onClick={() => onIndexChange(index + 1)}>
            {t('pelican.preview.next')}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
