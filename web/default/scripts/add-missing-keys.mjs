import fs from 'node:fs/promises'
import path from 'node:path'

const LOCALES_DIR = path.resolve('src/i18n/locales')

function stableStringify(obj) {
  return JSON.stringify(obj, null, 2) + '\n'
}

const newKeys = {
  en: {
    'Capacity soft-fail keywords': 'Capacity soft-fail keywords',
    'If an upstream error contains any of these keywords (case insensitive) and has a null/unknown error code or a 429/5xx status, treat it as temporary capacity overload: retry/switch channels without auto-disabling, and clear channel affinity.':
      'If an upstream error contains any of these keywords (case insensitive) and has a null/unknown error code or a 429/5xx status, treat it as temporary capacity overload: retry/switch channels without auto-disabling, and clear channel affinity.',
  },
  zh: {
    'Capacity soft-fail keywords': '容量软失败关键词',
    'If an upstream error contains any of these keywords (case insensitive) and has a null/unknown error code or a 429/5xx status, treat it as temporary capacity overload: retry/switch channels without auto-disabling, and clear channel affinity.':
      '当上游错误包含任一关键词（不区分大小写），且错误码为 null/unknown 或状态码为 429/5xx 时，视为临时容量过载：重试/切换渠道且不自动禁用，并清除渠道亲和。',
  },
  fr: {
    'Capacity soft-fail keywords': 'Mots-clés de soft-fail capacité',
    'If an upstream error contains any of these keywords (case insensitive) and has a null/unknown error code or a 429/5xx status, treat it as temporary capacity overload: retry/switch channels without auto-disabling, and clear channel affinity.':
      "Si une erreur amont contient l'un de ces mots-clés (insensible à la casse) avec un code null/inconnu ou un statut 429/5xx, la traiter comme une surcharge temporaire : réessayer/changer de canal sans désactivation auto, et effacer l'affinité de canal.",
  },
  ja: {
    'Capacity soft-fail keywords': '容量ソフトフェイルキーワード',
    'If an upstream error contains any of these keywords (case insensitive) and has a null/unknown error code or a 429/5xx status, treat it as temporary capacity overload: retry/switch channels without auto-disabling, and clear channel affinity.':
      '上流エラーにこれらのキーワード（大文字小文字を区別しない）のいずれかが含まれ、エラーコードが null/unknown またはステータスが 429/5xx の場合、一時的な容量超過として扱い、自動無効化せずに再試行/チャネル切替し、チャネル親和性をクリアします。',
  },
  ru: {
    'Capacity soft-fail keywords': 'Ключевые слова soft-fail по ёмкости',
    'If an upstream error contains any of these keywords (case insensitive) and has a null/unknown error code or a 429/5xx status, treat it as temporary capacity overload: retry/switch channels without auto-disabling, and clear channel affinity.':
      'Если ошибка upstream содержит любое из этих ключевых слов (без учёта регистра) и имеет код null/unknown или статус 429/5xx, считать это временной перегрузкой: повторять/переключать канал без авто-отключения и сбрасывать affinity канала.',
  },
  vi: {
    'Capacity soft-fail keywords': 'Từ khóa soft-fail dung lượng',
    'If an upstream error contains any of these keywords (case insensitive) and has a null/unknown error code or a 429/5xx status, treat it as temporary capacity overload: retry/switch channels without auto-disabling, and clear channel affinity.':
      'Nếu lỗi upstream chứa bất kỳ từ khóa nào (không phân biệt hoa thường) và có mã null/unknown hoặc trạng thái 429/5xx, coi là quá tải tạm thời: thử lại/chuyển kênh mà không tự tắt, đồng thời xóa channel affinity.',
  },
}

async function main() {
  let totalAdded = 0

  for (const [locale, trans] of Object.entries(newKeys)) {
    const filePath = path.join(LOCALES_DIR, `${locale}.json`)
    const json = JSON.parse(await fs.readFile(filePath, 'utf8'))

    let count = 0
    for (const [key, value] of Object.entries(trans)) {
      if (!Object.prototype.hasOwnProperty.call(json.translation, key)) {
        json.translation[key] = value
        count++
      } else if (json.translation[key] !== value) {
        json.translation[key] = value
        count++
      }
    }

    const sorted = Object.keys(json.translation)
      .sort((a, b) => a.localeCompare(b))
      .reduce((acc, key) => {
        acc[key] = json.translation[key]
        return acc
      }, {})
    json.translation = sorted

    await fs.writeFile(filePath, stableStringify(json), 'utf8')
    console.log(`${locale}: updated ${count} keys`)
    totalAdded += count
  }

  console.log(`Done. Total key writes: ${totalAdded}`)
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
