/** @file download_task.hpp
 * @brief Internal bounded-memory download job with in-process Range retries.
 * @warning Partial files are temporary and never treated as installed firmware.
 * Restarting this demo process restarts the download; MCU adapters may persist
 * their verified partial state according to flash and bootloader requirements.
 */
#ifndef FH_DOWNLOAD_TASK_HPP
#define FH_DOWNLOAD_TASK_HPP
#include "fw_client.h"
#include <QCryptographicHash>
#include <QNetworkAccessManager>
#include <QNetworkReply>
#include <QPointer>
#include <QRegularExpression>
#include <QSaveFile>
#include <QTimer>
#include <functional>

class DownloadTask : public QObject {
  public:
    DownloadTask(QNetworkAccessManager &network, const QNetworkRequest &request,
                 const fw_manifest &manifest, const QString &destination, QObject *parent)
        : QObject(parent), network_(network), request_(request), manifest_(manifest),
          file_(destination), hash_(QCryptographicHash::Sha256) {}
    std::function<void(QNetworkReply *)> onReply;
    std::function<void(qint64, qint64)> onProgress;
    std::function<void(const QString &)> onMessage;
    std::function<void(bool, const QString &)> onFinished;
    void begin() {
        file_.setDirectWriteFallback(false);
        if (!file_.open(QIODevice::WriteOnly)) {
            finish(false, "无法创建目标临时文件");
            return;
        }
        start();
    }
    void cancel() {
        finish(false, "操作已取消，原目标文件保持不变");
    }
    ~DownloadTask() override {
        if (reply_)
            reply_->abort();
    }

  private:
    void start() {
        if (closed_)
            return;
        responseValid_ = false;
        offset_ = count_;
        auto req = request_;
        if (offset_) {
            req.setRawHeader("Range", "bytes=" + QByteArray::number(offset_) + "-");
            req.setRawHeader("If-Range", etag());
        }
        auto reply = network_.get(req);
        reply_ = reply;
        reply->setParent(this);
        reply->setReadBufferSize(65536);
        if (onReply)
            onReply(reply);
        auto timeout = new QTimer(reply);
        timeout->setSingleShot(true);
        timeout->setInterval(30000);
        QObject::connect(timeout, &QTimer::timeout, reply, &QNetworkReply::abort);
        timeout->start();
        QObject::connect(reply, &QIODevice::readyRead, this, [this, reply, timeout] {
            timeout->start();
            consume(reply);
        });
        QObject::connect(reply, &QNetworkReply::finished, this, [this, reply] {
            if (closed_)
                return;
            consume(reply);
            if (closed_)
                return;
            const bool networkOK = reply->error() == QNetworkReply::NoError;
            const int status = reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt();
            reply->deleteLater();
            reply_.clear();
            if (onReply)
                onReply(nullptr);
            if (count_ == manifest_.size && responseValid_) {
                QByteArray digest = hash_.result();
                auto code = fw_verify_artifact(
                    &manifest_, reinterpret_cast<const uint8_t *>(digest.constData()), count_);
                if (code != FW_OK) {
                    finish(false, QString::fromLatin1(fw_result_string(code)));
                    return;
                }
                if (!file_.commit()) {
                    finish(false, "原子保存失败");
                    return;
                }
                finish(true, file_.fileName());
                return;
            }
            if (!networkOK && (status == 0 || status == 200 || status == 206) && attempts_ < 3) {
                ++attempts_;
                if (onMessage)
                    onMessage("连接中断，正在从 " + QString::number(count_) + " 字节续传");
                QTimer::singleShot(attempts_ * 400, this, [this] { start(); });
                return;
            }
            finish(false, "下载未完成或服务器拒绝请求，原目标文件保持不变");
        });
    }
    QByteArray etag() const {
        return '"' + QByteArray(reinterpret_cast<const char *>(manifest_.sha256), 32).toHex() + '"';
    }
    bool validate(QNetworkReply *r) {
        if (responseValid_)
            return true;
        const int status = r->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt();
        if (!status)
            return false;
        if ((status != 200 && status != 206) || r->rawHeader("ETag") != etag()) {
            finish(false, "HTTP 状态或固件 ETag 不匹配，拒绝拼接文件");
            return false;
        }
        if (status == 206) {
            auto match = QRegularExpression("^bytes ([0-9]+)-([0-9]+)/([0-9]+)$")
                             .match(QString::fromLatin1(r->rawHeader("Content-Range")));
            if (!match.hasMatch() || match.captured(1).toULongLong() != offset_ ||
                match.captured(3).toULongLong() != manifest_.size ||
                match.captured(2).toULongLong() < offset_ ||
                match.captured(2).toULongLong() >= manifest_.size) {
                finish(false, "续传区间不匹配");
                return false;
            }
        } else if (offset_) {
            if (!file_.resize(0) || !file_.seek(0)) {
                finish(false, "无法重新开始下载");
                return false;
            }
            hash_.reset();
            count_ = 0;
            offset_ = 0;
            if (onMessage)
                onMessage("服务器未接受 Range，已安全重新开始完整下载");
        }
        responseValid_ = true;
        return true;
    }
    void consume(QNetworkReply *r) {
        if (closed_ || !r->bytesAvailable() || !validate(r))
            return;
        while (r->bytesAvailable() > 0 && !closed_) {
            QByteArray bytes = r->read(32768);
            if (bytes.isEmpty())
                break;
            if (count_ + static_cast<quint64>(bytes.size()) > manifest_.size ||
                file_.write(bytes) != bytes.size()) {
                finish(false, "文件大小超限或写入失败");
                return;
            }
            hash_.addData(bytes);
            count_ += static_cast<quint64>(bytes.size());
            if (onProgress)
                onProgress(static_cast<qint64>(count_), static_cast<qint64>(manifest_.size));
        }
    }
    void finish(bool ok, const QString &text) {
        if (closed_)
            return;
        closed_ = true;
        if (reply_)
            reply_->abort();
        if (!ok)
            file_.cancelWriting();
        if (onFinished)
            onFinished(ok, text);
        deleteLater();
    }
    QNetworkAccessManager &network_;
    QNetworkRequest request_;
    fw_manifest manifest_;
    QSaveFile file_;
    QCryptographicHash hash_;
    QPointer<QNetworkReply> reply_;
    quint64 count_ = 0, offset_ = 0;
    int attempts_ = 0;
    bool responseValid_ = false, closed_ = false;
};
#endif
