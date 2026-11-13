#include "hub_client.hpp"
#include "download_task.hpp"
#include "monocypher-ed25519.h"
#include <QCryptographicHash>
#include <QDateTime>
#include <QFile>
#include <QFileInfo>
#include <QJsonDocument>
#include <QSaveFile>
#include <QSettings>
#include <QSharedPointer>
#include <QSslCertificate>
#include <QSslConfiguration>
#include <QSslSocket>
#include <array>
#include <cstring>

static int verifyEd25519(void *, const uint8_t key[32], const uint8_t signature[64],
                         const uint8_t *payload, size_t n) {
    return crypto_ed25519_check(signature, key, payload, n);
}
static bool hexBytes(const QByteArray &v, int count, QByteArray &out) {
    if (v.size() != count * 2)
        return false;
    for (char c : v)
        if (!((c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') || (c >= '0' && c <= '9')))
            return false;
    out = QByteArray::fromHex(v);
    return out.size() == count;
}
HubClient::HubClient(QObject *parent) : QObject(parent) {
    connect(&heartbeat_, &QTimer::timeout, this, &HubClient::heartbeat);
}
bool HubClient::configure(const Config &c) {
    if (busy())
        return false;
    ++epoch_;
    heartbeat_.stop();
    if (report_)
        report_->abort();
    config_ = c;
    hubTelemetry_ = false;
    downloadPath_.clear();
    package_ = QJsonObject();
    downloadedFile_.clear();
    return true;
}
void HubClient::setTelemetry(bool enabled) {
    config_.telemetry = enabled;
    if (!enabled) {
        heartbeat_.stop();
        if (report_)
            report_->abort();
    } else if (hubTelemetry_) {
        heartbeat_.start();
        heartbeat();
    }
}
bool HubClient::validConfig(QString &why) const {
    const QUrl &u = config_.base;
    if (!u.isValid() || u.host().isEmpty() || !u.userInfo().isEmpty() || !u.query().isEmpty() ||
        !u.fragment().isEmpty() || (u.path() != "" && u.path() != "/")) {
        why = "服务器地址必须是无凭据、无路径的 HTTP(S) 根地址";
        return false;
    }
    if (u.scheme() != "https" &&
        !(u.scheme() == "http" &&
          (u.host() == "127.0.0.1" || u.host() == "localhost" || u.host() == "::1"))) {
        why = "正式连接要求 HTTPS；HTTP 演示仅允许本机回环地址";
        return false;
    }
    if (u.scheme() == "https" && !QSslSocket::supportsSsl()) {
        why = "Qt SSL 后端不可用，请部署匹配的 OpenSSL 库";
        return false;
    }
    if (!config_.trustedCAFile.isEmpty() &&
        QSslCertificate::fromPath(config_.trustedCAFile).isEmpty()) {
        why = "指定的本地 CA 文件无效";
        return false;
    }
    return true;
}
QNetworkRequest HubClient::request(const QString &relative) const {
    QNetworkRequest r(config_.base.resolved(QUrl(relative)));
    r.setAttribute(QNetworkRequest::RedirectPolicyAttribute, QNetworkRequest::ManualRedirectPolicy);
    r.setRawHeader("Accept", "application/json");
    if (!config_.trustedCAFile.isEmpty()) {
        auto ssl = QSslConfiguration::defaultConfiguration();
        auto roots = ssl.caCertificates();
        roots.append(QSslCertificate::fromPath(config_.trustedCAFile));
        ssl.setCaCertificates(roots);
        r.setSslConfiguration(ssl);
    }
    if (!config_.token.isEmpty())
        r.setRawHeader("Authorization", "Bearer " + config_.token);
    return r;
}
void HubClient::check() {
    if (busy()) {
        emit failed("上一个操作尚未完成");
        return;
    }
    QString why;
    if (!validConfig(why)) {
        emit failed(why);
        return;
    }
    QByteArray project = config_.project.toUtf8(), channel = config_.channel.toUtf8(),
               platform = config_.platform.toUtf8(), arch = config_.arch.toUtf8(),
               hardware = config_.hardware.toUtf8();
    fw_policy p{};
    p.project = project.constData();
    p.channel = channel.constData();
    p.platform = platform.constData();
    p.arch = arch.constData();
    p.hardware = hardware.constData();
    p.current_sequence = config_.sequence;
    QByteArray installedVersion = config_.version.toUtf8();
    p.current_version = installedVersion.constData();
    p.minimum_security = config_.security;
    firmware::Client sdk(p);
    const std::string path = sdk.checkPath();
    if (path.empty()) {
        emit failed("项目或平台标识无效");
        return;
    }
    auto r = network_.get(request(QString::fromStdString(path)));
    active_ = r;
    QTimer::singleShot(20000, r, [r] {
        if (r->isRunning())
            r->abort();
    });
    auto data = QSharedPointer<QByteArray>::create();
    connect(r, &QIODevice::readyRead, this, [r, data] {
        *data += r->readAll();
        if (data->size() > 16384)
            r->abort();
    });
    const quint64 epoch = epoch_;
    connect(r, &QNetworkReply::finished, this, [this, r, data, epoch] {
        if (active_ == r)
            active_.clear();
        r->deleteLater();
        if (epoch != epoch_)
            return;
        if (r->error() != QNetworkReply::NoError ||
            r->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt() != 200) {
            emit failed("检查失败：网络、TLS 或访问权限错误；HTTP " +
                        r->attribute(QNetworkRequest::HttpStatusCodeAttribute).toString());
            return;
        }
        *data += r->readAll();
        if (data->size() > 16384) {
            emit failed("更新响应超过安全限制");
            return;
        }
        consumeCheck(*data);
    });
}
void HubClient::consumeCheck(const QByteArray &data) {
    QJsonParseError error;
    const auto doc = QJsonDocument::fromJson(data, &error);
    if (error.error != QJsonParseError::NoError || !doc.isObject()) {
        emit failed("服务返回无效 JSON");
        return;
    }
    const auto root = doc.object();
    const auto telemetry = root.value("telemetry").toObject();
    hubTelemetry_ = telemetry.value("enabled").toBool(false);
    int interval = qBound(15, telemetry.value("interval_seconds").toInt(60), 86400);
    heartbeat_.setInterval(interval * 1000);
    if (config_.telemetry && hubTelemetry_) {
        heartbeat_.start();
        heartbeat();
    } else
        heartbeat_.stop();
    downloadPath_.clear();
    package_ = QJsonObject();
    downloadedFile_.clear();
    if (!root.value("update").toBool(false)) {
        emit message("没有符合条件的新版本");
        emit checked(false);
        return;
    }
    auto release = root.value("release").toObject();
    const QString kind = release.value("package_kind").toString("full");
    if ((kind != "full" && kind != "file") ||
        (kind == "file" && release.value("base_version").toString() != config_.version)) {
        emit failed("更新方式或适用旧版本不匹配");
        return;
    }
    QByteArray payloadB64 = release.value("payload").toString().toLatin1();
    QByteArray payload = QByteArray::fromBase64(payloadB64);
    if (payload.toBase64() != payloadB64) {
        emit failed("清单 Base64 编码无效");
        return;
    }
    QByteArray signature, key;
    const bool signedMode = !config_.publicKey.isEmpty();
    if (signedMode && (!hexBytes(release.value("signature").toString().toLatin1(), 64, signature) ||
                       !hexBytes(config_.publicKey, 32, key))) {
        emit failed("签名或本地公钥格式无效");
        return;
    }
    QByteArray project = config_.project.toUtf8(), channel = config_.channel.toUtf8(),
               platform = config_.platform.toUtf8(), arch = config_.arch.toUtf8(),
               hardware = config_.hardware.toUtf8();
    fw_policy p{};
    p.project = project.constData();
    p.channel = channel.constData();
    p.platform = platform.constData();
    p.arch = arch.constData();
    p.hardware = hardware.constData();
    p.current_sequence = config_.sequence;
    p.minimum_security = config_.security;
    p.unix_time = static_cast<quint64>(QDateTime::currentSecsSinceEpoch());
    p.maximum_size = 1024ULL * 1024 * 1024;
    QSettings state;
    QString stateKey =
        "trust/" + QString(QCryptographicHash::hash(project + '\0' + key + '\0' + channel + '\0' +
                                                        platform + '\0' + arch + '\0' + hardware,
                                                    QCryptographicHash::Sha256)
                               .toHex());
    p.highest_seen_sequence = state.value(stateKey, 0).toULongLong();
    auto result =
        signedMode
            ? fw_verify_manifest(reinterpret_cast<const uint8_t *>(payload.constData()),
                                 static_cast<size_t>(payload.size()),
                                 reinterpret_cast<const uint8_t *>(signature.constData()),
                                 reinterpret_cast<const uint8_t *>(key.constData()), &p,
                                 verifyEd25519, nullptr, &manifest_)
            : fw_accept_server_manifest(reinterpret_cast<const uint8_t *>(payload.constData()),
                                        static_cast<size_t>(payload.size()), &p, &manifest_);
    if (result != FW_OK) {
        emit failed(QString::fromLatin1(fw_result_string(result)));
        return;
    }
    QString path = release.value("download").toString();
    QUrl endpoint(path);
    if (!path.startsWith("/api/v1/artifacts/") || !endpoint.isRelative() || endpoint.hasQuery() ||
        endpoint.hasFragment() || path.contains("..") || path.startsWith("//")) {
        emit failed("固件下载地址不符合受控同源策略");
        return;
    }
    state.setValue(stateKey, QVariant::fromValue<qulonglong>(manifest_.sequence));
    state.sync();
    if (state.status() != QSettings::NoError) {
        emit failed("无法持久化防回放状态");
        return;
    }
    downloadPath_ = path;
    package_ = release;
    emit message(signedMode
                     ? "数字签名、兼容范围与版本检查通过"
                     : "账号发布模式：兼容范围与版本检查通过，下载后校验 SHA-256（不是数字签名）");
    emit candidate(QString::fromLatin1(manifest_.version), manifest_.size);
    emit checked(true);
}
void HubClient::download(const QString &destination) {
    if (busy() || downloadPath_.isEmpty()) {
        emit failed("请先检查并验证一个可用更新");
        return;
    }
    auto job = new DownloadTask(network_, request(downloadPath_), manifest_, destination, this);
    downloadJob_ = job;
    job->onReply = [this](QNetworkReply *r) { active_ = r; };
    job->onProgress = [this](qint64 received, qint64 total) { emit progress(received, total); };
    job->onMessage = [this](const QString &m) { emit message(m); };
    job->onFinished = [this](bool ok, const QString &text) {
        active_.clear();
        downloadJob_.clear();
        if (ok) {
            downloadedFile_ = QFileInfo(text).absoluteFilePath();
            emit downloaded(text);
        } else
            emit failed(text);
    };
    job->begin();
}
QJsonObject HubClient::installationPlan(const QString &downloaded, const QString &directory) const {
    if (downloadedFile_.isEmpty() || QFileInfo(downloaded).absoluteFilePath() != downloadedFile_ ||
        package_.isEmpty())
        return {};
    const QString kind = package_.value("package_kind").toString("full");
    if (kind == "full" &&
        !package_.value("file_name").toString().endsWith(".zip", Qt::CaseInsensitive))
        return {};
    return {
        {"source", downloadedFile_},
        {"install_dir", QFileInfo(directory).absoluteFilePath()},
        {"sha256", QString::fromLatin1(
                       QByteArray(reinterpret_cast<const char *>(manifest_.sha256), 32).toHex())},
        {"size", static_cast<qint64>(manifest_.size)},
        {"project", config_.project},
        {"version", QString::fromLatin1(manifest_.version)},
        {"sequence", static_cast<qint64>(manifest_.sequence)},
        {"current_version", config_.version},
        {"package_kind", kind},
        {"base_version", package_.value("base_version").toString()},
        {"target_path", package_.value("target_path").toString()}};
}
QJsonObject HubClient::deviceImage(const QString &downloaded) const {
    if (downloadedFile_.isEmpty() || QFileInfo(downloaded).absoluteFilePath() != downloadedFile_ ||
        !package_.value("file_name").toString().endsWith(".bin", Qt::CaseInsensitive) ||
        package_.value("package_kind").toString("full") != "full")
        return {};
    return {
        {"source", downloadedFile_},
        {"sha256", QString::fromLatin1(
                       QByteArray(reinterpret_cast<const char *>(manifest_.sha256), 32).toHex())},
        {"version", QString::fromLatin1(manifest_.version)},
        {"hardware", config_.hardware}};
}
void HubClient::cancel() {
    if (downloadJob_)
        static_cast<DownloadTask *>(downloadJob_.data())->cancel();
    else if (active_)
        active_->abort();
}

void HubClient::heartbeat() {
    if (!config_.telemetry || !hubTelemetry_ || report_ || config_.token.isEmpty())
        return;
    auto r = request("/api/v1/update/" + config_.project + "/heartbeat");
    r.setHeader(QNetworkRequest::ContentTypeHeader, "application/json");
    auto data = QJsonDocument(QJsonObject{{"version", config_.version},
                                          {"platform", config_.platform},
                                          {"arch", config_.arch},
                                          {"hardware", config_.hardware},
                                          {"status", "idle"}})
                    .toJson(QJsonDocument::Compact);
    auto reply = network_.post(r, data);
    report_ = reply;
    QTimer::singleShot(15000, reply, [reply] {
        if (reply->isRunning())
            reply->abort();
    });
    const quint64 epoch = epoch_;
    connect(reply, &QNetworkReply::finished, this, [this, reply, epoch] {
        if (report_ == reply)
            report_.clear();
        reply->deleteLater();
        if (epoch != epoch_ || !config_.telemetry)
            return;
        int status = reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt();
        if (status == 403) {
            hubTelemetry_ = false;
            heartbeat_.stop();
            emit message("Hub 已关闭采集，客户端停止心跳");
        } else if (status == 200)
            emit reported();
        else
            emit message("状态上报未成功，不影响更新下载");
    });
}
