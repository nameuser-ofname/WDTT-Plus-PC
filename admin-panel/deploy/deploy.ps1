# Установка WDTT Admin на удалённый сервер по SSH (запускается из deploy.bat).
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
foreach ($t in 'ssh', 'tar') {
  if (-not (Get-Command $t -ErrorAction SilentlyContinue)) {
    throw "Не найден '$t'. Включите «Клиент OpenSSH» в Windows (Параметры → Приложения → Дополнительные компоненты)."
  }
}
function Ask($q, $def) { $v = Read-Host $(if ($def) { "$q [$def]" } else { $q }); if ([string]::IsNullOrWhiteSpace($v)) { $def } else { $v } }
function AskSecret($q) {
  $s = Read-Host $q -AsSecureString
  [Runtime.InteropServices.Marshal]::PtrToStringAuto([Runtime.InteropServices.Marshal]::SecureStringToBSTR($s))
}

$srvHost = Ask 'Адрес сервера (домен или IP)' ''
if (-not $srvHost) { throw 'Адрес не указан.' }
$user  = Ask 'SSH-пользователь' 'root'
$sshPort = Ask 'SSH-порт' '22'
$key   = Ask 'Путь к SSH-ключу (Enter = вход по паролю)' ''
$port  = Ask 'Порт панели (HTTPS)' '8443'
$main  = AskSecret 'Главный пароль WDTT-сервера (Enter = взять с сервера автоматически)'
$panel = AskSecret 'Пароль входа в панель (Enter = сгенерировать)'

$stage = Join-Path $env:TEMP ("wdtt-admin-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $stage | Out-Null
try {
  Copy-Item -Recurse (Join-Path $root 'bin') $stage
  $sh = [IO.File]::ReadAllText((Join-Path $root 'deploy.sh')) -replace "`r`n", "`n"
  [IO.File]::WriteAllText((Join-Path $stage 'deploy.sh'), $sh, (New-Object Text.UTF8Encoding($false)))
  $ans = "MAIN_PASSWORD=$main`nPANEL_PASSWORD=$panel`nPORT=$port`nHOST=$srvHost`n"
  [IO.File]::WriteAllText((Join-Path $stage 'answers.env'), $ans, (New-Object Text.UTF8Encoding($false)))
  $tgz = Join-Path $stage 'bundle.tgz'
  tar -czf $tgz -C $stage bin deploy.sh answers.env
  if ($LASTEXITCODE -ne 0) { throw 'tar завершился с ошибкой' }

  $sshArgs = @('-p', $sshPort, '-o', 'StrictHostKeyChecking=accept-new')
  if ($key) { $sshArgs += @('-i', $key) }
  $remote = 'set -e; D=$(mktemp -d); trap 'rm -rf $D' EXIT; tar -xz -C $D; chmod 600 $D/answers.env; bash $D/deploy.sh --answers $D/answers.env'
  Write-Host "`nПодключаюсь к $user@$srvHost (при необходимости введите SSH-пароль)...`n"
  $p = Start-Process -FilePath 'ssh' -ArgumentList ($sshArgs + @("$user@$srvHost", "`"$remote`"")) -RedirectStandardInput $tgz -NoNewWindow -Wait -PassThru
  if ($p.ExitCode -ne 0) { throw "Установка не удалась (код $($p.ExitCode))." }
} finally {
  Remove-Item -Recurse -Force $stage -ErrorAction SilentlyContinue
}
