import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { IconDownload, IconPhoto } from '@tabler/icons-react';
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

/** Loads a generated document through the authenticated API and renders it in a sandboxed iframe. */
export function PelicanPreviewFrame({ result, className }: { result: PelicanResult; className?: string }) {
  const { t } = useTranslation();
  const [url, setUrl] = useState<string>();
  const [failed, setFailed] = useState(false);

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

  if (result.status === 'running') {
    return <Skeleton className={cn('h-full w-full', className)} />;
  }
  if (failed || result.status !== 'succeeded') {
    return (
      <div className={cn('flex h-full w-full flex-col items-center justify-center gap-2 bg-muted/30 p-3 text-center', className)}>
        <span className="text-xs text-muted-foreground">{t('pelican.card.failed')}</span>
        {result.error ? <span className="text-[11px] leading-relaxed text-muted-foreground">{result.error}</span> : null}
      </div>
    );
  }
  if (!url) {
    return <Skeleton className={cn('h-full w-full', className)} />;
  }

  return (
    <iframe
      title={`${result.channelName ? `${result.channelName} ` : ''}${result.model} ${result.effort}`}
      src={url}
      sandbox="allow-scripts"
      referrerPolicy="no-referrer"
      className={cn('h-full w-full border-0 bg-white', className)}
    />
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
          <PelicanPreviewFrame result={result} />
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
                  <IconPhoto className="size-6 text-muted-foreground" />
                  <span className="text-sm text-muted-foreground">{t('pelican.card.failed')}</span>
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
