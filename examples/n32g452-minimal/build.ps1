param(
 [Parameter(Mandatory=$true)][string]$ArmBin,
 [Parameter(Mandatory=$true)][string]$SdkRoot,
 [string]$OutDir=(Join-Path $PSScriptRoot '../../build/n32g452')
)
$ErrorActionPreference='Stop'
$sampleRoot=$PSScriptRoot
$repoRoot=(Resolve-Path (Join-Path $sampleRoot '../..')).Path
$sdkBase=(Resolve-Path $SdkRoot).Path
$outputBase=[IO.Path]::GetFullPath($OutDir)
New-Item -ItemType Directory -Force $outputBase | Out-Null
$commonArgs=@('--cpu','Cortex-M4.fp','--c99','--apcs=interwork','-O2','--split_sections','-D','N32G45X','-D','USE_STDPERIPH_DRIVER')
foreach($inc in @($sampleRoot,"$sdkBase/CMSIS/core","$sdkBase/CMSIS/device","$sdkBase/n32g45x_std_periph_driver/inc","$repoRoot/sdk/rs485","$repoRoot/sdk/third_party/tinycrypt/include")){$commonArgs+=@('-I',$inc)}
function Invoke-Arm([string]$name,[string[]]$arguments){ & (Join-Path $ArmBin "$name.exe") @arguments; if($LASTEXITCODE -ne 0){throw "$name failed: $LASTEXITCODE"} }
foreach($targetName in @('boot','app-v1','app-v2')){
 $targetDir=Join-Path $outputBase $targetName
 New-Item -ItemType Directory -Force $targetDir | Out-Null
 $sources=@{port="$sampleRoot/port.c";fw485="$repoRoot/sdk/rs485/fw485.c";rcc="$sdkBase/n32g45x_std_periph_driver/src/n32g45x_rcc.c";gpio="$sdkBase/n32g45x_std_periph_driver/src/n32g45x_gpio.c";usart="$sdkBase/n32g45x_std_periph_driver/src/n32g45x_usart.c";flash="$sdkBase/n32g45x_std_periph_driver/src/n32g45x_flash.c"}
 if($targetName -eq 'boot'){$sources.boot="$sampleRoot/boot.c";$sources.sha256="$repoRoot/sdk/third_party/tinycrypt/src/sha256.c";$sources.utils="$repoRoot/sdk/third_party/tinycrypt/src/utils.c";$scatter="$sampleRoot/boot.sct"}else{$sources.app=if($targetName -eq 'app-v1'){"$sampleRoot/app_v1.c"}else{"$sampleRoot/app_v2.c"};$scatter="$sampleRoot/app.sct"}
 $objects=@()
 foreach($entry in $sources.GetEnumerator()){$obj=Join-Path $targetDir ($entry.Key+'.o');Invoke-Arm 'armcc' ($commonArgs+@('-c',$entry.Value,'-o',$obj));$objects+=$obj}
 $startup=Join-Path $targetDir 'startup.o'
 Invoke-Arm 'armasm' @('--cpu','Cortex-M4.fp','--apcs=interwork',"$sdkBase/CMSIS/device/startup/startup_n32g45x.s",'-o',$startup)
 $objects+=$startup
 $axf=Join-Path $targetDir "$targetName.axf"
 $linkArgs=@('--cpu','Cortex-M4.fp','--entry','Reset_Handler','--scatter',$scatter,'--map','--list',(Join-Path $targetDir 'link.map'),'--output',$axf)
 if($targetName -ne 'boot'){$linkArgs+=@('--keep','app.o(FW_INFO)')}
 Invoke-Arm 'armlink' ($linkArgs+$objects)
 Invoke-Arm 'fromelf' @('--bin','--output',(Join-Path $outputBase "$targetName.bin"),$axf)
 Invoke-Arm 'fromelf' @('--i32combined','--output',(Join-Path $outputBase "$targetName.hex"),$axf)
}
# Generated Intel HEX artifacts only. No flashing/debugger command is run here.
$hexLines=@(Get-Content (Join-Path $outputBase 'boot.hex') | Where-Object {$_ -ne ':00000001FF'})
$hexLines+=@(Get-Content (Join-Path $outputBase 'app-v1.hex') | Where-Object {$_ -ne ':00000001FF'})
$hexLines+=':00000001FF'
[IO.File]::WriteAllLines((Join-Path $outputBase 'initial-v1.hex'),$hexLines,[Text.Encoding]::ASCII)
Get-ChildItem $outputBase -Filter '*.bin' | Select-Object Name,Length
Write-Output 'Built reference binaries only. Initial programming needs an explicit board-qualified SWD/ISP procedure; this script never flashes hardware.'
