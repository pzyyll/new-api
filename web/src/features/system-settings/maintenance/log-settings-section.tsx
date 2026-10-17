// ABOUTME: Configures log recording, export limits, and file maintenance.
// ABOUTME: Keeps request detail controls alongside upstream log modules.
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
import { useCallback, useEffect, useId, useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
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
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'
import { api } from '@/lib/api'

import {
  SettingsControlGroup,
  SettingsForm,
  SettingsFormGrid,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import { safeNumberFieldProps } from '../utils/numeric-field'
import { HistoryLogCleanup } from './history-log-cleanup'
import { ServerLogFiles } from './server-log-files'

const logSettingsSchema = z.object({
  LogConsumeEnabled: z.boolean(),
  RequestDetailLogEnabled: z.boolean(),
  log_setting: z.object({
    user_export_enabled: z.boolean(),
    max_range_days: z.number().int().min(1),
  }),
})

type LogSettingsFormValues = z.infer<typeof logSettingsSchema>

type LogSettingsSectionProps = {
  defaultValues: {
    LogConsumeEnabled: boolean
    RequestDetailLogEnabled?: boolean
    'log_setting.user_export_enabled': boolean
    'log_setting.max_range_days': number
  }
}

type RequestDetailStorageInfo = {
  dir: string
  file_count: number
  total_size: number
}

function formatBytes(bytes: number, decimals = 2): string {
  if (!bytes || Number.isNaN(bytes)) return '0 Bytes'
  if (bytes === 0) return '0 Bytes'
  if (bytes < 0) return `-${formatBytes(-bytes, decimals)}`
  const k = 1024
  const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(Math.abs(bytes)) / Math.log(k))
  if (i < 0 || i >= sizes.length) return `${bytes} Bytes`
  return `${Number.parseFloat((bytes / Math.pow(k, i)).toFixed(decimals))} ${
    sizes[i]
  }`
}

export function LogSettingsSection(props: LogSettingsSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const [requestDetailInfo, setRequestDetailInfo] =
    useState<RequestDetailStorageInfo | null>(null)
  const [requestDetailClearLoading, setRequestDetailClearLoading] =
    useState(false)
  const [showRequestDetailConfirm, setShowRequestDetailConfirm] =
    useState(false)
  const recordingTitleId = useId()
  const searchTitleId = useId()
  const formDefaults = useMemo<LogSettingsFormValues>(
    () => ({
      LogConsumeEnabled: props.defaultValues.LogConsumeEnabled,
      RequestDetailLogEnabled:
        props.defaultValues.RequestDetailLogEnabled ?? false,
      log_setting: {
        user_export_enabled:
          props.defaultValues['log_setting.user_export_enabled'],
        max_range_days: props.defaultValues['log_setting.max_range_days'],
      },
    }),
    [props.defaultValues]
  )
  const form = useForm<LogSettingsFormValues>({
    resolver: zodResolver(logSettingsSchema),
    defaultValues: formDefaults,
  })

  useEffect(() => {
    form.reset(formDefaults)
  }, [formDefaults, form])

  const fetchRequestDetailInfo = useCallback(async () => {
    try {
      const res = await api.get('/api/log/request-detail/storage')
      if (res.data.success) setRequestDetailInfo(res.data.data)
    } catch {
      /* ignore */
    }
  }, [])

  useEffect(() => {
    fetchRequestDetailInfo()
  }, [fetchRequestDetailInfo])

  const clearRequestDetailFiles = async () => {
    setRequestDetailClearLoading(true)
    try {
      const res = await api.delete('/api/log/request-detail')
      if (!res.data.success) {
        throw new Error(
          res.data.message || t('Failed to clear request detail files')
        )
      }
      const removed = res.data.data?.removed ?? 0
      toast.success(
        t('Cleared {{count}} request detail directories.', { count: removed })
      )
      setShowRequestDetailConfirm(false)
      await fetchRequestDetailInfo()
    } catch (error) {
      const message =
        error instanceof Error
          ? error.message
          : t('Failed to clear request detail files')
      toast.error(message)
    } finally {
      setRequestDetailClearLoading(false)
    }
  }

  const onSubmit = async (values: LogSettingsFormValues) => {
    const changes = [
      ['LogConsumeEnabled', values.LogConsumeEnabled],
      ['RequestDetailLogEnabled', values.RequestDetailLogEnabled],
      [
        'log_setting.user_export_enabled',
        values.log_setting.user_export_enabled,
      ],
      ['log_setting.max_range_days', values.log_setting.max_range_days],
    ] as const
    for (const [key, value] of changes) {
      if (value === (props.defaultValues[key] ?? false)) continue
      await updateOption.mutateAsync({ key, value })
    }
  }

  return (
    <SettingsSection title={t('Log Maintenance')} className='gap-6'>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
            saveLabel='Save log settings'
          />
          <div
            role='group'
            aria-labelledby={recordingTitleId}
            className='flex min-w-0 flex-col gap-4'
          >
            <h4 id={recordingTitleId} className='font-medium'>
              {t('Log recording')}
            </h4>
            <FormField
              control={form.control}
              name='LogConsumeEnabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Record quota usage')}</FormLabel>
                    <FormDescription>
                      {t(
                        'Track per-request consumption to power usage analytics. Keeping this on increases database writes.'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                  <FormMessage />
                </SettingsSwitchItem>
              )}
            />
            <FormField
              control={form.control}
              name='RequestDetailLogEnabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Record request/response body')}</FormLabel>
                    <FormDescription>
                      {t(
                        'Store full request JSON and upstream response bodies as files under log-dir/request-detail/{request_id}/ for usage log details. Stream responses are stored as newline-delimited upstream data payloads. Not written to the database. Intended for temporary debugging; multi-node deployments need a shared log-dir.'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                  <FormMessage />
                </SettingsSwitchItem>
              )}
            />
          </div>

          <Separator />

          <div
            role='group'
            aria-labelledby={searchTitleId}
            className='flex min-w-0 flex-col gap-4'
          >
            <h4 id={searchTitleId} className='font-medium'>
              {t('Log search and export')}
            </h4>
            <SettingsFormGrid>
              <FormField
                control={form.control}
                name='log_setting.user_export_enabled'
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>{t('Allow users to export logs')}</FormLabel>
                      <FormDescription>
                        {t(
                          'Users can download their own logs from the usage log page. Administrators can always export.'
                        )}
                      </FormDescription>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                    <FormMessage />
                  </SettingsSwitchItem>
                )}
              />
              <FormField
                control={form.control}
                name='log_setting.max_range_days'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Longest log time range (days)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={1}
                        step={1}
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'The longest time range a user can view at once in the logs and on the dashboard, and the longest range any export can cover.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SettingsFormGrid>
          </div>
        </SettingsForm>
      </Form>

      <Separator />

      <SettingsControlGroup className='space-y-3'>
        <div>
          <h4 className='text-sm font-medium'>
            {t('Request detail body files')}
          </h4>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Clear all request/response body files stored for usage log details on this node.'
            )}
          </p>
        </div>
        {requestDetailInfo && (
          <div className='rounded-lg border p-3 text-sm'>
            <div className='grid grid-cols-1 gap-2 md:grid-cols-3'>
              <div>
                <span className='text-muted-foreground'>{t('Directory')}:</span>{' '}
                <span className='font-mono text-xs break-all'>
                  {requestDetailInfo.dir}
                </span>
              </div>
              <div>
                <span className='text-muted-foreground'>
                  {t('File Count')}:
                </span>{' '}
                {requestDetailInfo.file_count}
              </div>
              <div>
                <span className='text-muted-foreground'>
                  {t('Total Size')}:
                </span>{' '}
                {formatBytes(requestDetailInfo.total_size)}
              </div>
            </div>
          </div>
        )}
        <Button
          type='button'
          variant='destructive'
          size='sm'
          disabled={requestDetailClearLoading}
          onClick={() => setShowRequestDetailConfirm(true)}
        >
          {requestDetailClearLoading
            ? t('Clearing...')
            : t('Clear request detail files')}
        </Button>
        <ConfirmDialog
          open={showRequestDetailConfirm}
          onOpenChange={setShowRequestDetailConfirm}
          title={t('Clear all request detail body files?')}
          desc={t(
            'This permanently deletes all request/response body files under the request-detail directory on this node. Usage log rows are not affected.'
          )}
          destructive
          isLoading={requestDetailClearLoading}
          confirmText={t('Clear')}
          handleConfirm={clearRequestDetailFiles}
        />
      </SettingsControlGroup>

      <Separator />

      <HistoryLogCleanup />

      <Separator />

      <ServerLogFiles />
    </SettingsSection>
  )
}
