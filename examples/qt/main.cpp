#include "hub_client.hpp"
#include <QApplication>
#include <QCheckBox>
#include <QDateTime>
#include <QFile>
#include <QFileDialog>
#include <QFormLayout>
#include <QJsonDocument>
#include <QLabel>
#include <QLineEdit>
#include <QPlainTextEdit>
#include <QProgressBar>
#include <QPushButton>
#include <QSettings>
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
        c.version = "1.0.0";
        c.platform = "test";
        c.arch = "x64";
        c.hardware = "demo";
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
    auto intro = new QLabel("检查版本 → 独立验签 → 流式下载 → 摘要验证。此工具不执行安装或刷写。");
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
         key = field("预置公钥", "public_key", ""), version = field("当前版本", "version", "1.0.0"),
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
    QObject::connect(cancel, &QPushButton::clicked, &client, &HubClient::cancel);
    auto progress = new QProgressBar;
    progress->setValue(0);
    layout->addWidget(progress);
    auto log = new QPlainTextEdit;
    log->setReadOnly(true);
    layout->addWidget(log);
    auto append = [log](const QString &text) {
        log->appendPlainText(QDateTime::currentDateTime().toString("hh:mm:ss") + "  " + text);
    };
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
        append("下载和验签完成：" + p + "（未安装，当前版本不变）");
    });
    QObject::connect(&client, &HubClient::reported, &window,
                     [&] { append("当前版本和心跳已上报"); });
    QObject::connect(&client, &HubClient::progress, &window, [progress](qint64 n, qint64 total) {
        progress->setValue(total > 0 ? static_cast<int>(n * 100 / total) : 0);
    });
    window.show();
    return app.exec();
}
