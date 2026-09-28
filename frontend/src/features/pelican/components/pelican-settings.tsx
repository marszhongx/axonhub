import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { IconPlus, IconTrash } from '@tabler/icons-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';
import {
  PELICAN_EFFORTS,
  usePelicanChannels,
  usePelicanConfig,
  useSavePelicanConfig,
  type PelicanEffort,
  type PelicanTarget,
} from '../data/pelican';

/** Radix Select cannot hold an empty string, so "auto" stands in for "no reasoning_effort". */
const AUTO = 'auto';

/**
 * A row carries a client-only id: keying rows by their channel and model would remount the row
 * (closing the dropdown and dropping focus) on every change, and the index is not stable when a
 * row is removed. The id never reaches the API.
 */
type TargetRow = PelicanTarget & { id: string };

const toTarget = (row: TargetRow): PelicanTarget => ({ channel: row.channel, model: row.model, effort: row.effort });

export function PelicanSettingsDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t } = useTranslation();
  const { data: config } = usePelicanConfig();
  const channelsQuery = usePelicanChannels();
  const save = useSavePelicanConfig();

  const [prompt, setPrompt] = useState('');
  const [targets, setTargets] = useState<TargetRow[]>([]);
  const [scheduleEnabled, setScheduleEnabled] = useState(false);
  const rowCounter = useRef(0);
  const newRowID = () => `pelican-row-${(rowCounter.current += 1)}`;

  useEffect(() => {
    if (!config) return;
    setPrompt(config.prompt);
    setTargets(config.targets.map((target) => ({ ...target, id: newRowID() })));
    setScheduleEnabled(config.scheduleEnabled);
  }, [config]);

  const channels = useMemo(() => channelsQuery.data ?? [], [channelsQuery.data]);
  const channelById = useMemo(() => new Map(channels.map((channel) => [channel.id, channel])), [channels]);

  /** Models of the selected channel; the current value stays selectable if the channel dropped it. */
  const modelsFor = (target: TargetRow) => {
    const models = channelById.get(target.channel)?.models ?? [];
    return target.model && !models.includes(target.model) ? [...models, target.model] : models;
  };

  const updateTarget = (index: number, patch: Partial<PelicanTarget>) => {
    setTargets((current) => current.map((target, position) => (position === index ? { ...target, ...patch } : target)));
  };

  /** Switching the channel keeps the model only when that channel serves it too. */
  const selectChannel = (index: number, channelID: number) => {
    const models = channelById.get(channelID)?.models ?? [];
    setTargets((current) =>
      current.map((target, position) =>
        position === index
          ? { ...target, channel: channelID, model: models.includes(target.model) ? target.model : (models[0] ?? '') }
          : target
      )
    );
  };

  const addTarget = () => {
    const first = channels[0];
    setTargets((current) => [
      ...current,
      { id: newRowID(), channel: first?.id ?? 0, model: first?.models[0] ?? '', effort: '' },
    ]);
  };

  const incomplete = targets.some((target) => !target.channel || !target.model);

  const submit = async () => {
    try {
      await save.mutateAsync({ prompt, targets: targets.map(toTarget), scheduleEnabled });
      toast.success(t('pelican.settings.saved'));
      onOpenChange(false);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('pelican.settings.saveFailed'));
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[85vh] flex-col gap-4 overflow-y-auto sm:max-w-2xl">
        <DialogHeader className="pr-10">
          <DialogTitle>{t('pelican.settings.title')}</DialogTitle>
        </DialogHeader>

        <section className="flex flex-col gap-2">
          <div className="flex items-center justify-between">
            <Label htmlFor="pelican-prompt">{t('pelican.settings.prompt')}</Label>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={!config || prompt === config.defaultPrompt}
              onClick={() => setPrompt(config?.defaultPrompt ?? '')}
            >
              {t('pelican.settings.restoreDefault')}
            </Button>
          </div>
          <Textarea
            id="pelican-prompt"
            rows={5}
            maxLength={4000}
            value={prompt}
            onChange={(event) => setPrompt(event.target.value)}
            placeholder={t('pelican.settings.promptPlaceholder')}
          />
          <p className="text-xs text-muted-foreground">{t('pelican.settings.promptHint')}</p>
        </section>

        <section className="flex flex-col gap-3">
          <Label>{t('pelican.settings.targets')}</Label>
          <p className="text-xs text-muted-foreground">{t('pelican.settings.targetsHint')}</p>

          <div className="flex flex-col gap-2">
            {targets.length > 0 ? (
              <div className="hidden grid-cols-[8rem_minmax(0,1fr)_7rem_2.25rem] items-center gap-2 text-xs text-muted-foreground sm:grid">
                <span>{t('pelican.settings.channel')}</span>
                <span>{t('pelican.settings.model')}</span>
                <span>{t('pelican.settings.effort')}</span>
                <span aria-hidden />
              </div>
            ) : null}
            {targets.map((target, index) => (
              <div key={target.id} className="grid grid-cols-1 gap-2 sm:grid-cols-[8rem_minmax(0,1fr)_7rem_2.25rem] sm:items-center">
                <Select value={String(target.channel || '')} onValueChange={(value) => selectChannel(index, Number(value))}>
                  <SelectTrigger className="w-full min-w-0">
                    <SelectValue placeholder={t('pelican.settings.channelPlaceholder')} />
                  </SelectTrigger>
                  <SelectContent className="max-h-72">
                    {channels.map((channel) => (
                      <SelectItem key={channel.id} value={String(channel.id)}>
                        {channel.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Select value={target.model} onValueChange={(value) => updateTarget(index, { model: value })}>
                  <SelectTrigger className="w-full min-w-0">
                    <SelectValue className="truncate" placeholder={t('pelican.settings.modelPlaceholder')} />
                  </SelectTrigger>
                  <SelectContent className="max-h-72">
                    {modelsFor(target).map((model) => (
                      <SelectItem key={model} value={model}>
                        {model}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Select
                  value={target.effort === '' ? AUTO : target.effort}
                  onValueChange={(value) => updateTarget(index, { effort: (value === AUTO ? '' : value) as PelicanEffort })}
                >
                  <SelectTrigger className="w-full min-w-0">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {PELICAN_EFFORTS.map((effort) => (
                      <SelectItem key={effort || AUTO} value={effort || AUTO}>
                        {effort ? t(`pelican.efforts.${effort}`) : t('pelican.efforts.auto')}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="justify-self-end sm:justify-self-auto"
                  aria-label={t('pelican.settings.removeTarget')}
                  onClick={() => setTargets((current) => current.filter((_, position) => position !== index))}
                >
                  <IconTrash className="size-4" />
                </Button>
              </div>
            ))}
          </div>

          <Button type="button" variant="outline" size="sm" className="w-fit" onClick={addTarget}>
            <IconPlus className="size-4" />
            {t('pelican.settings.addTarget')}
          </Button>
        </section>

        <section className="flex items-start justify-between gap-4 rounded-md border p-3">
          <div className="flex flex-col gap-1">
            <Label htmlFor="pelican-schedule">{t('pelican.settings.schedule')}</Label>
            <p className="text-xs text-muted-foreground">{t('pelican.settings.scheduleHint')}</p>
          </div>
          <Switch id="pelican-schedule" checked={scheduleEnabled} onCheckedChange={setScheduleEnabled} />
        </section>

        {config?.dataDir ? (
          <p className="text-xs text-muted-foreground">{t('pelican.settings.dataDir', { dir: config.dataDir })}</p>
        ) : null}

        <DialogFooter>
          <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
            {t('pelican.settings.cancel')}
          </Button>
          <Button
            type="button"
            onClick={() => void submit()}
            disabled={save.isPending || !prompt.trim() || targets.length === 0 || incomplete}
          >
            {save.isPending ? t('pelican.settings.saving') : t('pelican.settings.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
