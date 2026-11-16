// Writes the fixture of internal/format/zim/glass: what libzim's writer does
// (xapianIndexer.cpp, xapianWorker.cpp), over documents a test can restate.
#include <xapian.h>
#include <string>
int main(int argc, char** argv) {
  std::string tmp = std::string(argv[1]) + ".tmp";
  {
    Xapian::WritableDatabase db(tmp, Xapian::DB_CREATE_OR_OVERWRITE | Xapian::DB_NO_TERMLIST | Xapian::DB_BACKEND_GLASS);
    db.set_metadata("valuesmap", "title:0;wordcount:1;geo.position:2");
    db.set_metadata("kind", "fulltext");
    db.set_metadata("data", "fullPath");
    db.set_metadata("language", "eng");
    std::string stops;
    for (int i = 0; i < 400; i++) stops += "stop" + std::to_string(i) + "\n";
    stops += "the\nof\n";
    db.set_metadata("stopwords", stops);
    Xapian::SimpleStopper stopper;
    stopper.add("the"); stopper.add("of");
    Xapian::Stem stem("en");
    for (int i = 1; i <= 3000; i++) {
      Xapian::TermGenerator tg;
      tg.set_flags(Xapian::TermGenerator::FLAG_CJK_NGRAM);
      tg.set_stemmer(stem);
      tg.set_stemming_strategy(Xapian::TermGenerator::STEM_ALL);
      tg.set_stopper(&stopper);
      tg.set_stopper_strategy(Xapian::TermGenerator::STOP_ALL);
      Xapian::Document doc;
      tg.set_document(doc);
      doc.set_data("C/doc" + std::to_string(i));
      std::string text = "the house of ";
      for (int k = 0; k <= i % 5; k++) text += "common ";
      text += "group" + std::to_string(i % 7) + " id" + std::to_string(i);
      text += (i % 2) ? " running quickly" : " walked slowly";
      if (i % 100 == 0) text += " rare 日本語";
      tg.index_text_without_positions(text);
      db.add_document(doc);
    }
    db.commit();
  }
  Xapian::Database src(tmp);
  src.compact(argv[1], Xapian::DBCOMPACT_SINGLE_FILE);
}
