#ifndef MAGENTIC_NOTCH_DARWIN_H
#define MAGENTIC_NOTCH_DARWIN_H

void createNotchWindowC(const char *document);
void destroyNotchWindowC(void);
void showNotchEventC(const char *payload);
void clearNotchEventC(const char *identifier);
void acknowledgeNotchEventC(const char *identifier);

#endif
