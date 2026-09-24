import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { IconRefresh, IconSettings, IconWand } from '@tabler/icons-react';
import { toast } from 'sonner';
import { Header } from '@/components/layout/header';
import { Main } from '@/components/layout/main';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { PelicanCard, PelicanPreviewDialog } from './components/pelican-preview';
import { PelicanSettingsDialog } from './components/pelican-settings';
import { usePelicanConfig, usePelicanResults, useRunPelicanRound } from './data/pelican';

export default function PelicanTest() {
  const { t } = useTranslation();
  const { data: config, refetch: refetchConfig } = usePelicanConfig();
  const { data, isLoading, refetch } = usePelicanResults();
  const run = useRunPelicanRound();
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [previewIndex, setPreviewIndex] = useState<number | null>(null);

  const results = data?.results ?? [];
  const configured = (config?.targets.length ?? 0) > 0;

  const startRound = async () => {
    try {
      await run.mutateAsync();
      toast.success(t('pelican.page.started'));
      await refetch();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('pelican.page.startFailed'));
    }
  };

  return (
    <>
      <Header fixed>
        <div className="flex flex-1 items-center justify-between gap-4">
          <div>
            <h1 className="text-lg font-semibold">{t('pelican.title')}</h1>
            <p className="text-xs text-muted-foreground">{t('pelican.subtitle')}</p>
          </div>
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => void refetch()} aria-label={t('pelican.page.refresh')}>
              <IconRefresh className="size-4" />
            </Button>
            <Button variant="outline" size="sm" onClick={() => setSettingsOpen(true)}>
              <IconSettings className="size-4" />
              {t('pelican.page.settings')}
            </Button>
            {/* Never disabled: clicking again starts another round in parallel. */}
            <Button size="sm" onClick={() => void startRound()} disabled={!configured}>
              <IconWand className="size-4" />
              {data?.running ? t('pelican.page.running') : t('pelican.page.run')}
            </Button>
          </div>
        </div>
      </Header>

      <Main fixed>
        {/* Main is overflow-hidden, so the content area carries the scrolling itself. */}
        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-4">
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-muted-foreground">
            <span>{t('pelican.page.summary', { total: data?.total ?? 0, succeeded: data?.succeeded ?? 0, failed: data?.failed ?? 0 })}</span>
            {config?.scheduleEnabled ? (
              <span>
                {t('pelican.page.nextRun')}: {config.nextRunAt ? new Date(config.nextRunAt).toLocaleString() : '-'}
              </span>
            ) : (
              <span>{t('pelican.page.scheduleOff')}</span>
            )}
          </div>

          {!configured && !isLoading ? (
            <Card className="flex flex-col items-center gap-3 p-8 text-center">
              <p className="text-sm text-muted-foreground">{t('pelican.page.emptyHint')}</p>
              <Button size="sm" onClick={() => setSettingsOpen(true)}>
                {t('pelican.page.settings')}
              </Button>
            </Card>
          ) : null}

          {isLoading ? (
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 2xl:grid-cols-5">
              {Array.from({ length: 6 }).map((_, index) => (
                <Skeleton key={index} className="h-72 w-full" />
              ))}
            </div>
          ) : null}

          {results.length > 0 ? (
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 2xl:grid-cols-5">
              {results.map((result, index) => (
                <PelicanCard key={result.id} result={result} onOpen={() => setPreviewIndex(index)} />
              ))}
            </div>
          ) : null}

          {!isLoading && configured && results.length === 0 ? (
            <Card className="flex flex-col items-center gap-2 p-8 text-center">
              <p className="text-sm text-muted-foreground">{t('pelican.page.noResults')}</p>
            </Card>
          ) : null}
        </div>
      </Main>

      {settingsOpen ? (
        <PelicanSettingsDialog
          open={settingsOpen}
          onOpenChange={(open) => {
            setSettingsOpen(open);
            if (!open) void refetchConfig();
          }}
        />
      ) : null}

      {previewIndex !== null && results.length > 0 ? (
        <PelicanPreviewDialog
          results={results}
          index={Math.min(previewIndex, results.length - 1)}
          onIndexChange={setPreviewIndex}
          onClose={() => setPreviewIndex(null)}
        />
      ) : null}
    </>
  );
}
