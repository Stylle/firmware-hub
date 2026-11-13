#include "hub_client.hpp"
#include <QApplication>
#include <QCheckBox>
#include <QDateTime>
#include <QFile>
#include <QFileDialog>
#include <QFormLayout>
#include <QGroupBox>
#include <QJsonDocument>
#include <QLabel>
#include <QLineEdit>
#include <QMessageBox>
#include <QPlainTextEdit>
#include <QProcess>
#include <QProgressBar>
#include <QPushButton>
#include <QRegularExpression>
#include <QSaveFile>
#include <QSettings>
#include <QTemporaryDir>
#include <QTimer>
#include <QVBoxLayout>
#include <QWidget>
#include <cstdio>

static void writeLine(FILE *stream, const QString &text) {
    QByteArray bytes = text.toUtf8();
    std::fwrite(bytes.constData(), 1, static_cast<size_t>(bytes.size()), stream);
    std::fputc('\n', stream);
    std::fflush(stream);
}

int main(int argc, char **argv) {
    QApplication app(argc, argv);
    app.setOrganizationName("FirmwareHub");
    app.setApplicationName("FirmwareHubDemo");
    if (qEnvironmentVariableIsSet("FH_STATE_DIR")) {
        QSettings::setDefaultFormat(QSettings::IniFormat);
        QSettings::setPath(QSettings::IniFormat, QSettings::UserScope,
                           qEnvironmentVariable("FH_STATE_DIR"));
    }
    HubClient client;
    // Automated integration mode uses environment variables, never command-line secrets.
    if (app.arguments().contains("--smoke")) {
        HubClient::Config c;
        c.base = QUrl(qEnvironmentVariable("FH_URL", "http://127.0.0.1:28183"));
        c.project = qEnvironmentVariable("FH_PROJECT", "demo-controller");
        c.publicKey = qgetenv("FH_PUBLIC_KEY");
        c.token = qgetenv("FH_TOKEN");
        c.trustedCAFile = qEnvironmentVariable("FH_CA_FILE");
        c.version = qEnvironmentVariable("FH_CURRENT_VERSION", "1.0.0");
        c.platform = qEnvironmentVariable("FH_PLATFORM", "test");
        c.arch = qEnvironmentVariable("FH_ARCH", "x64");
        c.hardware = qEnvironmentVariable("FH_HARDWARE", "demo");
        c.telemetry = qEnvironmentVariableIntValue("FH_TELEMETRY") == 1;
        client.configure(c);
        const QString output = qEnvironmentVariable("FH_OUTPUT", "downloaded-demo.bin");
        QObject::connect(&client, &HubClient::failed, &app, [&](const QString &reason) {
            writeLine(stderr, "FAIL: " + reason);
            app.exit(1);
        });
        QObject::connect(&client, &HubClient::message, &app,
                         [](const QString &m) { writeLine(stdout, m); });
        QObject::connect(&client, &HubClient::checked, &app, [&](bool yes) {
            if (yes)
                client.download(output);
            else
                app.exit(2);
        });
        QObject::connect(&client, &HubClient::downloaded, &app, [&](const QString &path) {
            writeLine(stdout, "VERIFIED_DOWNLOAD " + path);
            if (qEnvironmentVariableIsSet("FH_PLAN_DIR")) {
                const auto plan =
                    client.installationPlan(path, qEnvironmentVariable("FH_PLAN_DIR"));
                QSaveFile file(qEnvironmentVariable("FH_PLAN_FILE"));
                if (plan.isEmpty() || !file.open(QIODevice::WriteOnly) ||
                    file.write(QJsonDocument(plan).toJson()) < 0 || !file.commit()) {
                    writeLine(stderr, "FAIL: local plan creation failed");
                    app.exit(4);
                    return;
                }
                writeLine(stdout, "LOCAL_INSTALL_PLAN_READY");
            }
            QTimer::singleShot(1500, &app, [&] { app.exit(0); });
        });
        QTimer::singleShot(60000, &app, [&] { app.exit(3); });
        QTimer::singleShot(0, &client, &HubClient::check);
        return app.exec();
    }
    QWidget window;
    window.setWindowTitle("Firmware Hub · Qt 接入测试");
    window.resize(740, 790);
    auto layout = new QVBoxLayout(&window);
    auto title = new QLabel("固件接入测试 / Qt");
    title->setStyleSheet("font-size:24px;font-weight:600;color:#176b5a;margin:12px 0;");
    layout->addWidget(title);
    auto intro = new QLabel("检查版本 → 下载并校验 → "
                            "可选退出后安装软件。公钥留空为账号发布模式；填入公钥则严格验签。设备 "
                            "BIN 只下载，不自动刷写。");
    intro->setWordWrap(true);
    layout->addWidget(intro);
    auto form = new QFormLayout;
    layout->addLayout(form);
    QSettings settings;
    auto field = [&](const QString &label, const QString &key, const QString &fallback) {
        auto edit = new QLineEdit(settings.value(key, fallback).toString());
        form->addRow(label, edit);
        return edit;
    };
    auto base = field("Hub 地址", "url", "http://127.0.0.1:28183"),
         project = field("项目标识", "project", "demo-controller"),
         key = field("高级：预置公钥（可空）", "public_key", ""),
         version = field("当前版本", "version", "1.0.0"),
         sequence = field("已安装发布序号", "sequence", "0"),
         security = field("安全版本下限", "security", "0"),
         platform = field("平台", "platform", "test"), arch = field("架构", "arch", "x64"),
         hardware = field("硬件型号", "hardware", "demo");
    auto token = new QLineEdit;
    token->setEchoMode(QLineEdit::Password);
    form->addRow("设备凭据（不保存）", token);
    auto consent = new QCheckBox("允许向 Hub 上报当前版本、状态与心跳（默认关闭）");
    consent->setChecked(settings.value("telemetry", false).toBool());
    layout->addWidget(consent);
    auto buttons = new QHBoxLayout;
    layout->addLayout(buttons);
    auto check = new QPushButton("检查更新");
    auto download = new QPushButton("下载并验证");
    download->setEnabled(false);
    buttons->addWidget(check);
    buttons->addWidget(download);
    auto cancel = new QPushButton("取消操作");
    buttons->addWidget(cancel);
    auto install = new QPushButton("退出并安装软件更新");
    install->setEnabled(false);
    layout->addWidget(install);
    QString downloadedPath;
    QObject::connect(cancel, &QPushButton::clicked, &client, &HubClient::cancel);
    auto progress = new QProgressBar;
    progress->setValue(0);
    layout->addWidget(progress);
    auto log = new QPlainTextEdit;
    log->setReadOnly(true);
    log->setMaximumBlockCount(500);
    layout->addWidget(log);
    auto append = [log](const QString &text) {
        log->appendPlainText(QDateTime::currentDateTime().toString("hh:mm:ss") + "  " + text);
    };
    {
        QFile ledger(QCoreApplication::applicationDirPath() + "/.firmware-hub-version.json");
        if (ledger.open(QIODevice::ReadOnly) && ledger.size() < 2048) {
            const auto state = QJsonDocument::fromJson(ledger.readAll()).object();
            if (state.value("project").toString() == project->text()) {
                version->setText(state.value("version").toString());
                sequence->setText(QString::number(state.value("sequence").toDouble(), 'f', 0));
            }
        }
        QFile result(settings.value("last_update_result").toString());
        if (result.open(QIODevice::ReadOnly) && result.size() < 8192) {
            const auto value = QJsonDocument::fromJson(result.readAll()).object();
            append(value.value("success").toBool()
                       ? "上次本机软件安装成功（启动自检仍由应用负责）"
                       : "上次本机安装未完成：" + value.value("error").toString());
        }
    }
    auto serialToggle = new QCheckBox("显示最小 N32G452 示例设备测试（首次烧录新程序后使用）");
    layout->addWidget(serialToggle);
    auto serialGroup = new QGroupBox("RS485 / 新设备示例");
    serialGroup->setVisible(false);
    layout->addWidget(serialGroup);
    QObject::connect(serialToggle, &QCheckBox::toggled, serialGroup, &QGroupBox::setVisible);
    auto serialLayout = new QFormLayout(serialGroup);
    auto serialPort = new QLineEdit;
    serialPort->setPlaceholderText("例如 COM3");
    serialLayout->addRow("串口（9600 / 8N1 / 地址 1）", serialPort);
    auto serialInfo = new QLabel("不兼容未知旧 Bootloader；需先烧录 "
                                 "initial-v1.hex。仅操作此最小示例，不驱动任何产品业务输出。");
    serialInfo->setWordWrap(true);
    serialLayout->addRow(serialInfo);
    auto serialRead = new QPushButton("读取设备当前版本"),
         serialUpgrade = new QPushButton("将已下载 BIN 更新到设备");
    serialUpgrade->setEnabled(false);
    serialLayout->addRow(serialRead);
    serialLayout->addRow(serialUpgrade);
    auto serialProcess = new QProcess(&window);
    QByteArray serialOutput;
    bool readingSerial = false;
    auto startSerial = [&](const QStringList &args, bool readOnly) {
        if (serialProcess->state() != QProcess::NotRunning) {
            append("串口操作正在进行");
            return;
        }
        const QString tool = QCoreApplication::applicationDirPath() + "/firmware-hub-serial.exe";
        if (!QFileInfo::exists(tool) || serialPort->text().trimmed().isEmpty()) {
            append("请填写端口并部署 firmware-hub-serial.exe 和 Qt5SerialPort.dll");
            return;
        }
        serialOutput.clear();
        readingSerial = readOnly;
        serialRead->setEnabled(false);
        serialUpgrade->setEnabled(false);
        serialProcess->setProgram(tool);
        serialProcess->setArguments(args + QStringList{"--port", serialPort->text().trimmed()});
        serialProcess->start();
    };
    QObject::connect(serialProcess, &QProcess::readyReadStandardOutput, &window, [&] {
        const auto bytes = serialProcess->readAllStandardOutput();
        serialOutput += bytes;
        append(QString::fromUtf8(bytes).trimmed());
    });
    QObject::connect(serialProcess, &QProcess::readyReadStandardError, &window, [&] {
        append(QString::fromUtf8(serialProcess->readAllStandardError()).trimmed());
    });
    QObject::connect(serialProcess, &QProcess::errorOccurred, &window, [&](QProcess::ProcessError) {
        append("串口工具错误：" + serialProcess->errorString());
        serialRead->setEnabled(true);
    });
    QObject::connect(
        serialProcess, QOverload<int, QProcess::ExitStatus>::of(&QProcess::finished), &window,
        [&](int exit, QProcess::ExitStatus) {
            serialRead->setEnabled(true);
            serialUpgrade->setEnabled(!client.deviceImage(downloadedPath).isEmpty());
            if (exit == 0 && readingSerial) {
                const auto match = QRegularExpression("register 17 = (\\d+)")
                                       .match(QString::fromUtf8(serialOutput));
                if (match.hasMatch()) {
                    const int v = match.captured(1).toInt();
                    version->setText(QString("%1.%2.%3").arg(v / 100).arg(v / 10 % 10).arg(v % 10));
                    append("已从新示例设备读取实际版本，请重新检查更新。");
                }
            } else if (exit == 0)
                append("传输结束。请再次读取设备版本确认安装结果；下载版本不会自动记为已安装。");
        });
    QObject::connect(serialRead, &QPushButton::clicked, &window,
                     [&] { startSerial({"--read", "--register", "17", "--count", "2"}, true); });
    QObject::connect(serialUpgrade, &QPushButton::clicked, &window, [&] {
        const auto image = client.deviceImage(downloadedPath);
        if (image.value("hardware").toString() != "n32g452-demo") {
            append("当前不是新 N32G452 示例的固件；请检查项目和硬件型号");
            return;
        }
        if (QMessageBox::question(&window, "确认设备更新",
                                  "仅适用于已首次烧录新 Bootloader 的 N32G452 "
                                  "示例。将进入升级模式、分块传输、校验并切换程序。请保持供电，不要"
                                  "连接未知设备。继续？") != QMessageBox::Yes)
            return;
        startSerial({"--upload", image.value("source").toString(), "--sha256",
                     image.value("sha256").toString(), "--version",
                     image.value("version").toString(), "--hardware",
                     image.value("hardware").toString(), "--confirm-write", "--enter-boot",
                     "--activate"},
                    false);
    });
    auto importConfig = new QPushButton("导入连接配置");
    buttons->addWidget(importConfig);
    QObject::connect(importConfig, &QPushButton::clicked, &window, [&] {
        QString path = QFileDialog::getOpenFileName(&window, "导入本地测试连接配置", QString(),
                                                    "JSON (*.json)");
        if (path.isEmpty())
            return;
        QFile file(path);
        if (!file.open(QIODevice::ReadOnly) || file.size() > 8192) {
            append("连接配置不可读或过大");
            return;
        }
        auto document = QJsonDocument::fromJson(file.readAll());
        if (!document.isObject()) {
            append("连接配置不是有效的 JSON 对象");
            return;
        }
        auto config = document.object();
        base->setText(config.value("url").toString());
        project->setText(config.value("project").toString());
        key->setText(config.value("public_key").toString());
        token->setText(config.value("token").toString());
        if(config.contains("sequence"))sequence->setText(QString::number(config.value("sequence").toDouble(),'f',0));
        if(config.contains("security"))security->setText(QString::number(config.value("security").toDouble(),'f',0));
        for (auto pair :
             {qMakePair(QString("version"), version), qMakePair(QString("platform"), platform),
              qMakePair(QString("arch"), arch), qMakePair(QString("hardware"), hardware)})
            if (config.contains(pair.first))
                pair.second->setText(config.value(pair.first).toString());
        append("已导入连接配置。设备凭据仅在当前进程使用，不会保存到设置。");
    });
    QObject::connect(check, &QPushButton::clicked, &window, [&] {
        HubClient::Config c;
        c.base = QUrl(base->text().trimmed());
        c.project = project->text().trimmed();
        c.publicKey = key->text().trimmed().toLatin1();
        c.token = token->text().trimmed().toLatin1();
        c.version = version->text().trimmed();
        c.sequence = sequence->text().toULongLong();
        c.security = security->text().toULongLong();
        c.platform = platform->text().trimmed();
        c.arch = arch->text().trimmed();
        c.hardware = hardware->text().trimmed();
        c.telemetry = consent->isChecked();
        if (!client.configure(c)) {
            append("操作进行中，请稍后");
            return;
        }
        settings.setValue("url", c.base.toString());
        settings.setValue("project", c.project);
        settings.setValue("public_key", c.publicKey);
        settings.setValue("version", c.version);
        settings.setValue("sequence", c.sequence);
        settings.setValue("security", c.security);
        settings.setValue("platform", c.platform);
        settings.setValue("arch", c.arch);
        settings.setValue("hardware", c.hardware);
        settings.setValue("telemetry", c.telemetry);
        download->setEnabled(false);
        install->setEnabled(false);
        serialUpgrade->setEnabled(false);
        downloadedPath.clear();
        client.check();
    });
    QObject::connect(consent, &QCheckBox::toggled, &window, [&](bool enabled) {
        settings.setValue("telemetry", enabled);
        client.setTelemetry(enabled);
        append(enabled ? "客户端同意状态上报；仍需 Hub 允许" : "客户端关闭状态上报");
    });
    QObject::connect(download, &QPushButton::clicked, &window, [&] {
        const QString path =
            QFileDialog::getSaveFileName(&window, "保存经过验证的固件", "firmware.bin");
        if (!path.isEmpty())
            client.download(path);
    });
    QObject::connect(&client, &HubClient::message, &window, append);
    QObject::connect(&client, &HubClient::failed, &window,
                     [&](const QString &text) { append("错误：" + text); });
    QObject::connect(&client, &HubClient::candidate, &window, [&](const QString &v, quint64 bytes) {
        append("发现可信版本 " + v + "，" + QString::number(bytes) + " 字节");
        download->setEnabled(true);
    });
    QObject::connect(&client, &HubClient::downloaded, &window, [&](const QString &p) {
        downloadedPath = p;
        serialUpgrade->setEnabled(!client.deviceImage(p).isEmpty());
        install->setEnabled(
            !client.installationPlan(p, QCoreApplication::applicationDirPath()).isEmpty());
        append("下载和完整性校验完成：" + p + "（尚未安装，当前版本不变）");
    });
    QObject::connect(install, &QPushButton::clicked, &window, [&] {
        const QString dir =
            QFileDialog::getExistingDirectory(&window, "选择需要更新的软件目录（不会自动猜测）");
        if (dir.isEmpty())
            return;
        const auto plan = client.installationPlan(downloadedPath, dir);
        if (plan.isEmpty()) {
            append("该文件不是可安装的软件包");
            return;
        }
        if (QMessageBox::question(&window, "确认软件更新",
                                  "将退出此测试程序，由独立更新器替换所选目录中的文件。请先关闭目标"
                                  "软件。原文件会保留备份；不会执行包内脚本。继续？") !=
            QMessageBox::Yes)
            return;
        const QString helper = QCoreApplication::applicationDirPath() + "/firmware-hub-update.exe";
        if (!QFileInfo::exists(helper)) {
            append("缺少独立更新器 firmware-hub-update.exe");
            return;
        }
        QTemporaryDir helperDirectory;
        if (!helperDirectory.isValid()) {
            append("无法创建独立更新目录");
            return;
        }
        const QString helperCopy = helperDirectory.filePath("firmware-hub-update.exe");
        if (!QFile::copy(helper, helperCopy)) {
            append("无法将更新器复制到软件目录外");
            return;
        }
        const QString planFile = helperDirectory.filePath("package.install.json");
        QSaveFile file(planFile);
        if (!file.open(QIODevice::WriteOnly) || file.write(QJsonDocument(plan).toJson()) < 0 ||
            !file.commit()) {
            append("无法保存本地安装计划");
            return;
        }
        if (!QProcess::startDetached(helperCopy,
                                     {"--plan", planFile, "--wait-pid",
                                      QString::number(QCoreApplication::applicationPid())})) {
            append("无法启动更新器");
            return;
        }
        helperDirectory.setAutoRemove(false);
        settings.setValue("last_update_result", planFile + ".result.json");
        settings.sync();
        app.quit();
    });
    QObject::connect(&client, &HubClient::reported, &window,
                     [&] { append("当前版本和心跳已上报"); });
    QObject::connect(&client, &HubClient::progress, &window, [progress](qint64 n, qint64 total) {
        progress->setValue(total > 0 ? static_cast<int>(n * 100 / total) : 0);
    });
    window.show();
    return app.exec();
}
