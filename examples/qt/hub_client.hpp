/** @file hub_client.hpp
 * @brief Qt 5.14+/Qt 6 reference transport for Firmware Hub.
 * @details Keeps UI responsive, bounds metadata, refuses redirects and invalid
 * TLS, streams artifacts to a temporary file, verifies before atomic commit.
 * This is a download/verification adapter, not a universal firmware flasher.
 */
#ifndef HUB_CLIENT_HPP
#define HUB_CLIENT_HPP
#include "fw_client.hpp"
#include <QJsonObject>
#include <QNetworkAccessManager>
#include <QNetworkReply>
#include <QObject>
#include <QPointer>
#include <QTimer>
#include <QUrl>

class HubClient : public QObject {
    Q_OBJECT
  public:
    struct Config {
        QUrl base;
        QByteArray token, publicKey; /**< Empty publicKey selects account/HTTPS mode locally.
                                        Nonempty always requires a valid signature. */
        QString trustedCAFile;       /**< Optional explicit local CA; never fetched from Hub. */
        QString project, version = "0.0.0", platform = "any", arch = "any", hardware = "any",
                         channel = "stable";
        quint64 sequence = 0, security = 0;
        bool telemetry = false; /**< Explicit local consent; default off. */
    };
    explicit HubClient(QObject *parent = nullptr);
    /** Reconfigure only while no check/download is in progress. Stops heartbeat. */
    bool configure(const Config &config);
    /** GET metadata; verifies using provisioned key before offering an artifact. */
    void check();
    /** Download the already verified candidate; never executes the file. */
    void download(const QString &destination);
    /** Cancel an active check/download without replacing the destination file. */
    void cancel();
    /** Change local consent immediately. Turning off aborts an in-flight report. */
    void setTelemetry(bool enabled);
    /** Send a heartbeat only after local and Hub permission are both known. */
    void heartbeat();
    /** Build a LOCAL desktop install plan only after verified download. The caller
     * chooses the application directory and invokes the separate update helper
     * after the main program exits. No remote command is executed. */
    QJsonObject installationPlan(const QString &downloadedFile,
                                 const QString &applicationDirectory) const;
    /** Metadata for an explicitly integrated serial bridge; empty for ZIP/file updates. */
    QJsonObject deviceImage(const QString &downloadedFile) const;
    bool busy() const {
        return !active_.isNull() || !downloadJob_.isNull();
    }
  signals:
    void message(const QString &text);
    void candidate(const QString &version, quint64 size);
    void progress(qint64 received, qint64 total);
    void downloaded(const QString &path);
    void failed(const QString &reason);
    void checked(bool available);
    void reported();

  private:
    QNetworkRequest request(const QString &relative) const;
    void consumeCheck(const QByteArray &json);
    bool validConfig(QString &reason) const;
    Config config_;
    QNetworkAccessManager network_;
    QPointer<QNetworkReply> active_, report_;
    QPointer<QObject> downloadJob_;
    QTimer heartbeat_;
    fw_manifest manifest_{};
    QString downloadPath_;
    QJsonObject package_;
    QString downloadedFile_;
    bool hubTelemetry_ = false;
    quint64 epoch_ = 0;
};
#endif
