// ABOUTME: Configures retries, latency routing, and automated channel tests.
// ABOUTME: Validates alpha reliability settings together with concurrent testing.
/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { zodResolver } from '@hookform/resolvers/zod'
import { useMemo, useRef } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { parseHttpStatusCodeRules } from '@/lib/http-status-code-rules'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'
import { safeNumberFieldProps } from '../utils/numeric-field'

const numericString = z.string().refine((value) => {
  const trimmed = value.trim()
  if (!trimmed) return true
  return !Number.isNaN(Number(trimmed)) && Number(trimmed) >= 0
}, 'Enter a non-negative number or leave empty')

const channelTestModes = [
  'scheduled_all',
  'auto_ban_only',
  'passive_recovery',
] as const
type ChannelTestMode = (typeof channelTestModes)[number]
const MAX_CHANNEL_TEST_CONCURRENCY = 32
const MAX_SAME_CHANNEL_RETRIES = 5
const MAX_RETRY_DELAY_MS = 60000
const MAX_TTFT_SAMPLES = 10000
const MIN_TTFT_FACTOR = 0.1
const MAX_TTFT_FACTOR = 10
const MAX_TTFT_REFERENCE_MS = 600000

const createRoutingReliabilitySchema = (
  t: (key: string, options?: Record<string, unknown>) => string
) =>
  z
    .object({
      RetryTimes: z.coerce.number().min(0).max(10),
      SameChannelRetryTimes: z.coerce.number().min(0).max(MAX_SAME_CHANNEL_RETRIES),
      SameChannelRetryBaseDelayMs: z.coerce.number().min(0).max(MAX_RETRY_DELAY_MS),
      SameChannelRetryMaxDelayMs: z.coerce.number().min(0).max(MAX_RETRY_DELAY_MS),
      TtftRoutingEnabled: z.boolean(),
      TtftRoutingMinSamples: z.coerce.number().int().min(1).max(MAX_TTFT_SAMPLES),
      TtftRoutingMinFactor: z.coerce.number().min(MIN_TTFT_FACTOR).max(MAX_TTFT_FACTOR),
      TtftRoutingMaxFactor: z.coerce.number().min(MIN_TTFT_FACTOR).max(MAX_TTFT_FACTOR),
      TtftRoutingRefMs: z.coerce.number().int().min(1).max(MAX_TTFT_REFERENCE_MS),
      UpstreamCapacityKeywords: z.string(),
      ChannelDisableThreshold: numericString,
      AutomaticDisableChannelEnabled: z.boolean(),
      AutomaticEnableChannelEnabled: z.boolean(),
      AutomaticDisableKeywords: z.string(),
      AutomaticDisableStatusCodes: z.string(),
      AutomaticRetryStatusCodes: z.string(),
      monitor_setting: z.object({
        auto_test_channel_enabled: z.boolean(),
        auto_test_channel_minutes: z.coerce
          .number()
          .int()
          .min(1, t('Interval must be at least 1 minute')),
        channel_test_concurrency: z.coerce
          .number()
          .int(t('Enter a positive integer'))
          .min(1, t('Channel test concurrency must be between 1 and 32'))
          .max(
            MAX_CHANNEL_TEST_CONCURRENCY,
            t('Channel test concurrency must be between 1 and 32')
          ),
        channel_test_mode: z.enum(channelTestModes),
      }),
    })
    .superRefine((values, ctx) => {
      if (values.TtftRoutingMaxFactor < values.TtftRoutingMinFactor) {
        ctx.addIssue({
          code: 'custom',
          path: ['TtftRoutingMaxFactor'],
          message: t('Max factor must be greater than or equal to min factor'),
        })
      }
      const disableParsed = parseHttpStatusCodeRules(
        values.AutomaticDisableStatusCodes
      )
      if (!disableParsed.ok) {
        ctx.addIssue({
          code: 'custom',
          path: ['AutomaticDisableStatusCodes'],
          message: t('Invalid status code rules: {{tokens}}', {
            tokens: disableParsed.invalidTokens.join(', '),
          }),
        })
      }

      const retryParsed = parseHttpStatusCodeRules(
        values.AutomaticRetryStatusCodes
      )
      if (!retryParsed.ok) {
        ctx.addIssue({
          code: 'custom',
          path: ['AutomaticRetryStatusCodes'],
          message: t('Invalid status code rules: {{tokens}}', {
            tokens: retryParsed.invalidTokens.join(', '),
          }),
        })
      }
    })

type RoutingReliabilitySchema = ReturnType<
  typeof createRoutingReliabilitySchema
>
type RoutingReliabilityFormValues = z.output<RoutingReliabilitySchema>
type RoutingReliabilityFormInput = z.input<RoutingReliabilitySchema>

type RoutingReliabilitySectionProps = {
  defaultValues: {
    RetryTimes: number
    SameChannelRetryTimes: number
    SameChannelRetryBaseDelayMs: number
    SameChannelRetryMaxDelayMs: number
    TtftRoutingEnabled: boolean
    TtftRoutingMinSamples: number
    TtftRoutingMinFactor: number
    TtftRoutingMaxFactor: number
    TtftRoutingRefMs: number
    ChannelDisableThreshold: string
    AutomaticDisableChannelEnabled: boolean
    AutomaticEnableChannelEnabled: boolean
    AutomaticDisableKeywords: string
    UpstreamCapacityKeywords: string
    AutomaticDisableStatusCodes: string
    AutomaticRetryStatusCodes: string
    'monitor_setting.auto_test_channel_enabled': boolean
    'monitor_setting.auto_test_channel_minutes': number
    'monitor_setting.channel_test_concurrency': number
    'monitor_setting.channel_test_mode': ChannelTestMode
  }
}

function normalizeLineEndings(value: string) {
  return value.replaceAll('\r\n', '\n')
}

type NormalizedRoutingReliabilityValues = {
  RetryTimes: number
  SameChannelRetryTimes: number
  SameChannelRetryBaseDelayMs: number
  SameChannelRetryMaxDelayMs: number
  TtftRoutingEnabled: boolean
  TtftRoutingMinSamples: number
  TtftRoutingMinFactor: number
  TtftRoutingMaxFactor: number
  TtftRoutingRefMs: number
  ChannelDisableThreshold: string
  AutomaticDisableChannelEnabled: boolean
  AutomaticEnableChannelEnabled: boolean
  AutomaticDisableKeywords: string
  UpstreamCapacityKeywords: string
  AutomaticDisableStatusCodes: string
  AutomaticRetryStatusCodes: string
  'monitor_setting.auto_test_channel_enabled': boolean
  'monitor_setting.auto_test_channel_minutes': number
  'monitor_setting.channel_test_concurrency': number
  'monitor_setting.channel_test_mode': ChannelTestMode
}

function normalizeChannelTestMode(value?: string): ChannelTestMode {
  if (value === 'auto_ban_only' || value === 'passive_recovery') {
    return value
  }
  return 'scheduled_all'
}

const buildFormDefaults = (
  defaults: RoutingReliabilitySectionProps['defaultValues']
): RoutingReliabilityFormInput => ({
  RetryTimes: defaults.RetryTimes ?? 0,
  SameChannelRetryTimes: defaults.SameChannelRetryTimes ?? 0,
  SameChannelRetryBaseDelayMs: defaults.SameChannelRetryBaseDelayMs ?? 200,
  SameChannelRetryMaxDelayMs: defaults.SameChannelRetryMaxDelayMs ?? 2000,
  TtftRoutingEnabled: defaults.TtftRoutingEnabled ?? false,
  TtftRoutingMinSamples: defaults.TtftRoutingMinSamples ?? 20,
  TtftRoutingMinFactor: defaults.TtftRoutingMinFactor ?? 0.5,
  TtftRoutingMaxFactor: defaults.TtftRoutingMaxFactor ?? 2.0,
  TtftRoutingRefMs: defaults.TtftRoutingRefMs ?? 500,
  ChannelDisableThreshold: defaults.ChannelDisableThreshold ?? '',
  AutomaticDisableChannelEnabled: defaults.AutomaticDisableChannelEnabled,
  AutomaticEnableChannelEnabled: defaults.AutomaticEnableChannelEnabled,
  AutomaticDisableKeywords: normalizeLineEndings(
    defaults.AutomaticDisableKeywords ?? ''
  ),
  UpstreamCapacityKeywords: normalizeLineEndings(
    defaults.UpstreamCapacityKeywords ?? ''
  ),
  AutomaticDisableStatusCodes: defaults.AutomaticDisableStatusCodes ?? '',
  AutomaticRetryStatusCodes: defaults.AutomaticRetryStatusCodes ?? '',
  monitor_setting: {
    auto_test_channel_enabled:
      defaults['monitor_setting.auto_test_channel_enabled'],
    auto_test_channel_minutes:
      defaults['monitor_setting.auto_test_channel_minutes'],
    channel_test_concurrency:
      defaults['monitor_setting.channel_test_concurrency'],
    channel_test_mode: normalizeChannelTestMode(
      defaults['monitor_setting.channel_test_mode']
    ),
  },
})

const normalizeDefaults = (
  defaults: RoutingReliabilitySectionProps['defaultValues']
): NormalizedRoutingReliabilityValues => ({
  RetryTimes: defaults.RetryTimes ?? 0,
  SameChannelRetryTimes: defaults.SameChannelRetryTimes ?? 0,
  SameChannelRetryBaseDelayMs: defaults.SameChannelRetryBaseDelayMs ?? 200,
  SameChannelRetryMaxDelayMs: defaults.SameChannelRetryMaxDelayMs ?? 2000,
  TtftRoutingEnabled: defaults.TtftRoutingEnabled ?? false,
  TtftRoutingMinSamples: defaults.TtftRoutingMinSamples ?? 20,
  TtftRoutingMinFactor: defaults.TtftRoutingMinFactor ?? 0.5,
  TtftRoutingMaxFactor: defaults.TtftRoutingMaxFactor ?? 2.0,
  TtftRoutingRefMs: defaults.TtftRoutingRefMs ?? 500,
  ChannelDisableThreshold: (defaults.ChannelDisableThreshold ?? '').trim(),
  AutomaticDisableChannelEnabled: defaults.AutomaticDisableChannelEnabled,
  AutomaticEnableChannelEnabled: defaults.AutomaticEnableChannelEnabled,
  AutomaticDisableKeywords: normalizeLineEndings(
    defaults.AutomaticDisableKeywords ?? ''
  ),
  UpstreamCapacityKeywords: normalizeLineEndings(
    defaults.UpstreamCapacityKeywords ?? ''
  ),
  AutomaticDisableStatusCodes: parseHttpStatusCodeRules(
    defaults.AutomaticDisableStatusCodes ?? ''
  ).normalized,
  AutomaticRetryStatusCodes: parseHttpStatusCodeRules(
    defaults.AutomaticRetryStatusCodes ?? ''
  ).normalized,
  'monitor_setting.auto_test_channel_enabled':
    defaults['monitor_setting.auto_test_channel_enabled'],
  'monitor_setting.auto_test_channel_minutes':
    defaults['monitor_setting.auto_test_channel_minutes'],
  'monitor_setting.channel_test_concurrency':
    defaults['monitor_setting.channel_test_concurrency'],
  'monitor_setting.channel_test_mode': normalizeChannelTestMode(
    defaults['monitor_setting.channel_test_mode']
  ),
})

const normalizeFormValues = (
  values: RoutingReliabilityFormValues
): NormalizedRoutingReliabilityValues => ({
  RetryTimes: values.RetryTimes,
  SameChannelRetryTimes: values.SameChannelRetryTimes,
  SameChannelRetryBaseDelayMs: values.SameChannelRetryBaseDelayMs,
  SameChannelRetryMaxDelayMs: values.SameChannelRetryMaxDelayMs,
  TtftRoutingEnabled: values.TtftRoutingEnabled,
  TtftRoutingMinSamples: values.TtftRoutingMinSamples,
  TtftRoutingMinFactor: values.TtftRoutingMinFactor,
  TtftRoutingMaxFactor: values.TtftRoutingMaxFactor,
  TtftRoutingRefMs: values.TtftRoutingRefMs,
  ChannelDisableThreshold: values.ChannelDisableThreshold.trim(),
  AutomaticDisableChannelEnabled: values.AutomaticDisableChannelEnabled,
  AutomaticEnableChannelEnabled: values.AutomaticEnableChannelEnabled,
  AutomaticDisableKeywords: normalizeLineEndings(
    values.AutomaticDisableKeywords
  ),
  UpstreamCapacityKeywords: normalizeLineEndings(
    values.UpstreamCapacityKeywords
  ),
  AutomaticDisableStatusCodes: parseHttpStatusCodeRules(
    values.AutomaticDisableStatusCodes
  ).normalized,
  AutomaticRetryStatusCodes: parseHttpStatusCodeRules(
    values.AutomaticRetryStatusCodes
  ).normalized,
  'monitor_setting.auto_test_channel_enabled':
    values.monitor_setting.auto_test_channel_enabled,
  'monitor_setting.auto_test_channel_minutes':
    values.monitor_setting.auto_test_channel_minutes,
  'monitor_setting.channel_test_concurrency':
    values.monitor_setting.channel_test_concurrency,
  'monitor_setting.channel_test_mode': values.monitor_setting.channel_test_mode,
})

export function RoutingReliabilitySection({
  defaultValues,
}: RoutingReliabilitySectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const routingReliabilitySchema = createRoutingReliabilitySchema(t)
  const baselineRef = useRef<NormalizedRoutingReliabilityValues>(
    normalizeDefaults(defaultValues)
  )

  const formDefaults = useMemo(
    () => buildFormDefaults(defaultValues),
    [defaultValues]
  )

  const form = useForm<
    RoutingReliabilityFormInput,
    unknown,
    RoutingReliabilityFormValues
  >({
    resolver: zodResolver(routingReliabilitySchema),
    defaultValues: formDefaults,
  })

  useResetForm(form, formDefaults)

  const autoDisableStatusCodes = form.watch('AutomaticDisableStatusCodes')
  const autoRetryStatusCodes = form.watch('AutomaticRetryStatusCodes')
  const channelTestMode = form.watch('monitor_setting.channel_test_mode')
  let channelTestModeDescription: string
  switch (channelTestMode) {
    case 'auto_ban_only':
      channelTestModeDescription = t(
        'Periodically checks only channels with auto-disable enabled, excluding manually disabled channels.'
      )
      break
    case 'passive_recovery':
      channelTestModeDescription = t(
        'Does not check healthy channels. It only rechecks auto-disabled channels and restores them after they recover.'
      )
      break
    default:
      channelTestModeDescription = t(
        'Periodically checks all channels except manually disabled ones to detect failures and recover channels automatically.'
      )
  }
  const autoDisableParsed = useMemo(
    () => parseHttpStatusCodeRules(autoDisableStatusCodes),
    [autoDisableStatusCodes]
  )
  const autoRetryParsed = useMemo(
    () => parseHttpStatusCodeRules(autoRetryStatusCodes),
    [autoRetryStatusCodes]
  )

  const onSubmit = async (values: RoutingReliabilityFormValues) => {
    const normalized = normalizeFormValues(values)
    const updates = (
      Object.keys(normalized) as Array<keyof NormalizedRoutingReliabilityValues>
    ).filter((key) => normalized[key] !== baselineRef.current[key])

    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }

    for (const key of updates) {
      const value = normalized[key]
      await updateOption.mutateAsync({
        key,
        value,
      })
    }

    baselineRef.current = normalized
  }

  return (
    <SettingsSection title={t('Routing Reliability')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
          />

          <div className='flex min-w-0 flex-col gap-4'>
            <div className='flex flex-col gap-1'>
              <h4 className='text-sm font-medium'>{t('Request retry')}</h4>
            </div>
            <div className='grid min-w-0 gap-6 xl:grid-cols-[minmax(12rem,24rem)_minmax(0,1fr)]'>
              <FormField
                control={form.control}
                name='RetryTimes'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Retry Times')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min='0'
                        max='10'
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Number of channel switches after failure (0-10). Same-channel retries do not consume this budget.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='SameChannelRetryTimes'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Same-channel retry times')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min='0'
                        max='5'
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Extra attempts on the same channel with exponential backoff for transient errors (network, 429, 502, 503). 0 disables.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='SameChannelRetryBaseDelayMs'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Same-channel base delay (ms)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min='0'
                        max='60000'
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Initial backoff delay in milliseconds before the first same-channel retry.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='SameChannelRetryMaxDelayMs'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Same-channel max delay (ms)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min='0'
                        max='60000'
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Maximum exponential backoff delay in milliseconds for same-channel retries.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='AutomaticRetryStatusCodes'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Auto-retry status codes')}</FormLabel>
                    <FormControl>
                      <Input
                        placeholder={t('e.g. 401, 403, 429, 500-599')}
                        value={field.value}
                        onChange={(event) => field.onChange(event.target.value)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Accepts comma-separated status codes and inclusive ranges.'
                      )}{' '}
                      {autoRetryParsed.ok &&
                        autoRetryParsed.normalized &&
                        autoRetryParsed.normalized !== field.value.trim() && (
                          <span className='text-muted-foreground'>
                            {t('Normalized:')} {autoRetryParsed.normalized}
                          </span>
                        )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='UpstreamCapacityKeywords'
                render={({ field }) => (
                  <FormItem className='xl:col-span-2'>
                    <FormLabel>{t('Capacity soft-fail keywords')}</FormLabel>
                    <FormControl>
                      <Textarea
                        rows={6}
                        placeholder={t('one keyword per line')}
                        {...field}
                        onChange={(event) => field.onChange(event.target.value)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'If an upstream error contains any of these keywords (case insensitive) and has a null/unknown error code or a 429/5xx status, treat it as temporary capacity overload: retry/switch channels without auto-disabling, and clear channel affinity.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
          </div>

          <div className='flex min-w-0 flex-col gap-4'>
            <div className='flex flex-col gap-1'>
              <h4 className='text-sm font-medium'>
                {t('TTFT-weighted routing')}
              </h4>
              <p className='text-muted-foreground text-sm'>
                {t(
                  'Bias same-priority channel selection toward lower first-token latency. Stats are keyed by channel + model name (e.g. gpt-4o).'
                )}
              </p>
            </div>
            <div className='grid min-w-0 gap-6 xl:grid-cols-[minmax(12rem,24rem)_minmax(0,1fr)]'>
              <FormField
                control={form.control}
                name='TtftRoutingEnabled'
                render={({ field }) => (
                  <FormItem>
                    <SettingsSwitchItem>
                      <SettingsSwitchContent>
                        <FormLabel>{t('Enable TTFT routing bias')}</FormLabel>
                        <FormDescription>
                          {t(
                            'When off, only configured channel weights are used within a priority tier.'
                          )}
                        </FormDescription>
                      </SettingsSwitchContent>
                      <FormControl>
                        <Switch
                          checked={field.value}
                          onCheckedChange={field.onChange}
                        />
                      </FormControl>
                    </SettingsSwitchItem>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='TtftRoutingMinSamples'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('TTFT min samples')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min='1'
                        max='10000'
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Minimum successful stream samples per channel+model before latency can bias weight.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='TtftRoutingRefMs'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('TTFT reference (ms)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min='1'
                        max='600000'
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Fallback reference first-token latency when peer median is unavailable.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='TtftRoutingMinFactor'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('TTFT min weight factor')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min='0.1'
                        max='10'
                        step='0.1'
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Lower clamp for the latency multiplier (slower channels).'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='TtftRoutingMaxFactor'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('TTFT max weight factor')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min='0.1'
                        max='10'
                        step='0.1'
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Upper clamp for the latency multiplier (faster channels).'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
          </div>

          <Separator />

          <div className='flex min-w-0 flex-col gap-4'>
            <div className='flex flex-col gap-1'>
              <h4 className='text-sm font-medium'>
                {t('Channel health checks')}
              </h4>
            </div>
            <div className='grid min-w-0 gap-6 lg:grid-cols-3'>
              <FormField
                control={form.control}
                name='monitor_setting.auto_test_channel_enabled'
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>{t('Scheduled channel tests')}</FormLabel>
                      <FormDescription>
                        {t(
                          'Automatically probe all channels in the background'
                        )}
                      </FormDescription>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                  </SettingsSwitchItem>
                )}
              />

              <FormField
                control={form.control}
                name='monitor_setting.channel_test_mode'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Channel test mode')}</FormLabel>
                    <Select
                      items={[
                        {
                          value: 'scheduled_all',
                          label: t('Actively check all channels'),
                        },
                        {
                          value: 'auto_ban_only',
                          label: t(
                            'Actively check auto-disable-enabled channels'
                          ),
                        },
                        {
                          value: 'passive_recovery',
                          label: t('Check channels awaiting recovery only'),
                        },
                      ]}
                      value={field.value}
                      onValueChange={field.onChange}
                    >
                      <FormControl>
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent alignItemWithTrigger={false}>
                        <SelectGroup>
                          <SelectItem value='scheduled_all'>
                            {t('Actively check all channels')}
                          </SelectItem>
                          <SelectItem value='auto_ban_only'>
                            {t('Actively check auto-disable-enabled channels')}
                          </SelectItem>
                          <SelectItem value='passive_recovery'>
                            {t('Check channels awaiting recovery only')}
                          </SelectItem>
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FormDescription>
                      {channelTestModeDescription}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='monitor_setting.auto_test_channel_minutes'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Test interval (minutes)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={1}
                        step={1}
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {channelTestMode === 'passive_recovery'
                        ? t(
                            'How frequently the system checks auto-disabled channels for recovery'
                          )
                        : t('How frequently the system tests all channels')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='monitor_setting.channel_test_concurrency'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Channel test concurrency')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={1}
                        max={MAX_CHANNEL_TEST_CONCURRENCY}
                        step={1}
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Maximum number of channels tested at the same time (1-32)'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='AutomaticEnableChannelEnabled'
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>{t('Re-enable on success')}</FormLabel>
                      <FormDescription>
                        {t(
                          'Bring channels back online after successful checks'
                        )}
                      </FormDescription>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                  </SettingsSwitchItem>
                )}
              />
            </div>
          </div>

          <Separator />

          <div className='flex min-w-0 flex-col gap-4'>
            <div className='flex flex-col gap-1'>
              <h4 className='text-sm font-medium'>{t('Auto-disable rules')}</h4>
            </div>
            <div className='grid min-w-0 gap-6 lg:grid-cols-2'>
              <FormField
                control={form.control}
                name='AutomaticDisableChannelEnabled'
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>{t('Disable on failure')}</FormLabel>
                      <FormDescription>
                        {t('Automatically disable channels when tests fail')}
                      </FormDescription>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                  </SettingsSwitchItem>
                )}
              />

              <FormField
                control={form.control}
                name='ChannelDisableThreshold'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Disable threshold (seconds)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={0}
                        step={1}
                        value={field.value}
                        onChange={(event) => field.onChange(event.target.value)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Automatically disable channels exceeding this response time'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='AutomaticDisableStatusCodes'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Auto-disable status codes')}</FormLabel>
                    <FormControl>
                      <Input
                        placeholder={t('e.g. 401, 403, 429, 500-599')}
                        value={field.value}
                        onChange={(event) => field.onChange(event.target.value)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Accepts comma-separated status codes and inclusive ranges.'
                      )}{' '}
                      {autoDisableParsed.ok &&
                        autoDisableParsed.normalized &&
                        autoDisableParsed.normalized !== field.value.trim() && (
                          <span className='text-muted-foreground'>
                            {t('Normalized:')} {autoDisableParsed.normalized}
                          </span>
                        )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='AutomaticDisableKeywords'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Failure keywords')}</FormLabel>
                    <FormControl>
                      <Textarea
                        rows={6}
                        placeholder={t('one keyword per line')}
                        {...field}
                        onChange={(event) => field.onChange(event.target.value)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'If an upstream error contains any of these keywords (case insensitive), the channel will be disabled automatically.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
          </div>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
